package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"codie-video-player/internal/codieout"
	"codie-video-player/internal/i18n"
	"codie-video-player/internal/video"
)

// 在 codie 上播放:剧集转成设备会话收的 MP4(H.264/AAC,≤1080p,≤128MiB),
// 传给设备的媒体会话,画面就落在它的屏幕上;播放、进度、音量都走会话的动作,
// 状态由轮询回来看看。

// codieMaxFileBytes is what one session file may take (the firmware's own
// cap), for the check before the upload.
const codieMaxFileBytes = 128 << 20

// codieTargetSegmentBytes is the size budget per split piece when an episode
// transcodes past codieMaxFileBytes, leaving headroom for bitrate spikes.
const codieTargetSegmentBytes = 90 << 20

// codieSegment is one MP4 piece of an episode split to fit under the device's
// 128MiB session cap. Together, codieSegs play back-to-back as one episode.
type codieSegment struct {
	path    string
	startMS int64 // cumulative start offset of this segment in the whole file
	durMS   int64 // duration of this segment's file in milliseconds
}

// codieOpenSessionFn opens a media session on the device; a var so tests can
// observe segment uploads and transitions.
var codieOpenSessionFn = func(c *codieout.Client, path string, req codieout.MediaSessionRequest) (string, error) {
	return c.CreateMediaSession(path, req)
}

// codieMediaAction is how the app asks the device's session to do something
// (pause, seek, volume); a var so tests can hear what the app asks for.
var codieMediaAction = func(c *codieout.Client, session, action string, args map[string]any) {
	go c.MediaAction(session, action, args)
}

// findFFmpegFn finds the ffmpeg binary, a var so tests can take the making
// of a file away.
var findFFmpegFn = findFFmpeg

// codieFFmpegTimeout is how long making the episode's MP4 may take.
const codieFFmpegTimeout = 20 * time.Minute

// probeTimeout is how long one ffprobe ask may take: a probe of an address
// the network has left hanging must not hang the playback with it.
const probeTimeout = 20 * time.Second

// codiePollEvery is how often the window asks the device what the session is
// doing.
const codiePollEvery = 800 * time.Millisecond

// playEpisodeOnDevice makes the episode into an MP4 the device takes and puts
// it on the device's screen. resumeS is where the story was left, in seconds
// (0 plays from the top); the episode runs at whatever the app's rate is now,
// which is baked into the file, for the device itself has no say over its
// speed.
func (a *app) playEpisodeOnDevice(item video.Item, ep video.Episode, at int, s *video.Stream, resumeS float64, resolve func() (*video.Stream, error)) {
	rate := a.rate
	var gen uint64
	ctx, cancel := context.WithCancel(context.Background())
	a.post(func() {
		a.stopPlayer()
		a.codieCancel = cancel
		gen = a.codieGen
		a.playerErr = ""
		a.playing = playing{item: item, ep: ep, at: at}
		a.page = pagePlay
		a.codieBusy = i18n.T("Transcoding for the device…")
	})

	log.Printf("codie play: transcoding started")
	totalS := a.codieProbeSeconds(s, ep)
	if totalS > 0 {
		// 这一集在设备文件时钟里的全长:进度条从第一秒起就是整个视频的。
		a.post(func() {
			if a.codieGen == gen {
				a.codieEpisodeMS = int64(totalS / rate * 1000)
			}
		})
	}
	if totalS <= 0 {
		// Nothing to cut by: the whole episode is made first, as before.
		dst, err := a.codieMakeMP4(ctx, s, rate)
		log.Printf("codie play: transcode done (err=%v)", err)
		if err != nil {
			a.post(func() {
				if a.codieGen != gen {
					return
				}
				a.codieBusy = ""
				a.playerErr = videoProblem(err, i18n.T("This episode won’t play"))
			})
			return
		}
		a.codieShow(dst, rate, resumeS, gen)
		return
	}
	a.codieStream(ctx, s, rate, resumeS, totalS, gen, resolve)
}

// codieSegmentSpanS is how much of an episode the first segment holds: short,
// so the picture comes soon, and the size it made decides the rest's spans.
// codieStallTicks is how many checks (700ms each) a pass may write nothing
// before it's taken for dead: 60 seconds of a silent ffmpeg.
const codieStallTicks = 85

const codieSegmentSpanS = 30

// codieResolveFresh is how long a stream's address is taken as young: longer
// than this and the next segment asks for a fresh one.
const codieResolveFresh = 90 * time.Second

// codieFirstSegmentBytes and codieSegmentBytes are what a segment aims at:
// about 10 MiB for the first, so it's quickly here, and 90 MiB after — close
// under the device's one-session cap, since every extra segment is another
// seam to carry over.
const (
	codieFirstSegmentBytes = 10 << 20
	codieSegmentBytes      = 90 << 20
)

// codieSeamMargin is the headroom a seam starts over the measured upload
// time: the picture gives up at most this much of a segment's tail when the
// next lands early, and shows at most this much of a stall when it's late.
const codieSeamMargin = 800 * time.Millisecond

// codieUploadSeed is the upload time assumed before any has been measured:
// generous, for a black beat hurts more than a shaved beat of tail.
const codieUploadSeed = 4 * time.Second

// codiePollFast is how often the device is asked once a segment nears its
// end: the seam's timing is only as good as the tick that fires it.
const codiePollFast = 250 * time.Millisecond

// codieStream makes the episode in segments, each cut to stay under the
// device's one-session cap, and plays the first as soon as it's made: the rest
// follow while the first is on the screen.
func (a *app) codieStream(ctx context.Context, s *video.Stream, rate, resumeS, totalS float64, gen uint64, resolve func() (*video.Stream, error)) {
	from := resumeS
	span := float64(codieSegmentSpanS)
	made := 0
	resolvedAt := time.Now()
	for from < totalS {
		if ctx.Err() != nil {
			return // stopped: nothing more is made for it
		}
		want := span
		if rest := totalS - from; rest < want {
			want = rest
		}
		// A signed address ages: ask again before a segment that starts well
		// after the last ask, rather than waiting for its doors to close.
		if resolve != nil && made > 0 && time.Since(resolvedAt) > codieResolveFresh {
			if fresh, rerr := resolve(); rerr == nil {
				s, resolvedAt = fresh, time.Now()
			}
		}
		seg, err := a.codieMakeSegment(ctx, s, rate, from, want)
		if err != nil && ctx.Err() == nil && resolve != nil {
			// An address ages: its doors close after a while, or after so
			// many opens. One fresh ask is worth a retry before giving up.
			if again, rerr := resolve(); rerr == nil {
				log.Printf("codie play: segment %d (%.0fs+%.0fs) failed (%v); retrying with a fresh stream", made, from, want, err)
				s = again
				seg, err = a.codieMakeSegment(ctx, s, rate, from, want)
			}
		}
		if err != nil {
			if ctx.Err() != nil {
				return // stopped while it was being made
			}
			log.Printf("codie play: segment %d (%.0fs+%.0fs) failed: %v", made, from, want, err)
			idx := made
			reason := err.Error()
			a.post(func() {
				if a.codieGen != gen {
					return
				}
				if idx == 0 {
					a.codieBusy = ""
					a.playerErr = videoProblem(err, i18n.T("This episode won’t play"))
					return
				}
				// Later segments failing leaves what was made playing; say
				// why rather than falling silent.
				a.codieMore = false
				a.codieBusy = i18n.Tf("The rest of this episode couldn’t be made: %s", reason)
			})
			return
		}
		// A span that came out over the device's cap is cut in half and made
		// again, so no segment is too big for one session.
		if size := fileSize(seg.path); size > codieMaxFileBytes && want > 5 {
			log.Printf("codie play: segment %d is %d bytes, over the cap; halving %.0fs", made, size, want)
			os.Remove(seg.path)
			span = want / 2
			continue
		}
		size := fileSize(seg.path)
		log.Printf("codie play: segment %d made (%.0fs+%.0fs, %d bytes)", made, from, want, size)
		// 这一段每秒出了多少字节,下一段就按它定长:第一段够小,后面按 30MiB。
		if size > 0 && want > 0 {
			perSec := float64(size) / want
			target := float64(codieSegmentBytes)
			next := target / perSec
			if next < 15 {
				next = 15
			}
			if next > 300 {
				next = 300
			}
			span = next
		}
		from += want
		if totalS > 0 {
			pct := int(from / totalS * 100)
			if pct > 100 {
				pct = 100
			}
			a.post(func() {
				if a.codieBusy != "" && made > 0 {
					a.codieBusy = i18n.Tf("Transcoding for the device… %d%% · %s", pct, megabytes(fileSize(seg.path)))
				}
			})
		}
		if made == 0 {
			if !a.codieStartFirst(seg, gen, rate, from) {
				removeCodieSegments([]codieSegment{seg})
				return
			}
		} else {
			s := seg
			a.post(func() { a.codieSegs = append(a.codieSegs, s) })
		}
		made++
	}
	a.post(func() {
		if a.codieGen == gen {
			a.codieMore = false
		}
	})
}

// codieStartFirst opens the device's session on the first segment and lets it
// play, saying whether it took it.
func (a *app) codieStartFirst(seg codieSegment, gen uint64, rate, madeToS float64) bool {
	req := codieout.MediaSessionRequest{
		Layout:         codieout.FullscreenLayout(),
		Playback:       &codieout.MediaPlayback{Volume: a.vol / 100},
		ConflictPolicy: "replace",
	}
	if want := a.slotChoice; want > 0 && want != a.deviceSlot && a.codie != nil {
		if err := a.codie.UseSlot(want); err != nil {
			log.Printf("codie play: switch slot %d: %v", want, err)
		} else {
			a.post(func() { a.deviceSlot = want })
		}
	}
	t0 := time.Now()
	session, err := codieOpenSessionFn(a.codie, seg.path, req)
	took := time.Since(t0)
	log.Printf("codie play: first segment session=%q err=%v (upload %v)", session, err, took)
	if err != nil {
		return false
	}
	a.post(func() {
		if a.codieGen != gen {
			if a.codie != nil {
				go a.codie.CloseMediaSession(session)
			}
			return
		}
		a.codieUploadTook(took)
		a.codieBusy = ""
		a.codieSegs = []codieSegment{seg}
		a.codieSegAt = 0
		a.codieTotalMS = 0
		a.codieSession = session
		a.codieFile = seg.path
		a.codieMore = true
		if a.rate != rate {
			go a.codieRespeed(rate, a.rate)
			return
		}
		a.codiePoll(session)
	})
	return true
}

// codieMakeSegment makes one segment of the episode: the span from fromS, in
// the shape the device takes.
func (a *app) codieMakeSegment(ctx context.Context, s *video.Stream, rate, fromS, spanS float64) (codieSegment, error) {
	ffmpeg := findFFmpegFn()
	if ffmpeg == "" {
		return codieSegment{}, errors.New(i18n.T("The device needs ffmpeg to take episodes; install it with your package manager and try again"))
	}
	f, err := os.CreateTemp("", "codie-seg-*.mp4")
	if err != nil {
		return codieSegment{}, err
	}
	dst := f.Name()
	f.Close()
	args := codieSegmentArgs(s, rate, fromS, spanS, subtitleFile(s), dst)
	runCtx, cancel := context.WithTimeout(ctx, codieFFmpegTimeout)
	defer cancel()
	cmd := exec.CommandContext(runCtx, ffmpeg, args...)
	hideWindow(cmd)
	cmd.Env = proxyEnv()
	// 转的过程中说到做到多少 MB,免得界面只剩一个转圈的圈。
	stop := make(chan struct{})
	var lastSize int64 = -1
	idle := 0
	go func() {
		tick := time.NewTicker(700 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-stop:
				return
			case <-tick.C:
				n := fileSize(dst)
				// 一直不长,就是源头没东西进来:停下来说清楚,别让界面一直转圈。
				if n == lastSize {
					idle++
					if idle >= codieStallTicks {
						log.Printf("codie play: no data after %ds, giving up", idle/2*700/1000)
						if cmd.Process != nil {
							cmd.Process.Kill()
						}
						return
					}
				} else {
					idle, lastSize = 0, n
				}
				a.post(func() {
					if a.codieBusy != "" {
						a.codieBusy = i18n.Tf("Transcoding for the device… %s", megabytes(n))
					}
				})
			}
		}
	}()
	var said sayBuffer
	cmd.Stdout = &said
	cmd.Stderr = &said
	err = cmd.Run()
	close(stop)
	if err != nil {
		os.Remove(dst)
		if ctx.Err() != nil {
			return codieSegment{}, ctx.Err() // the playback is over, not a failure
		}
		if runCtx.Err() != nil {
			return codieSegment{}, errors.New(i18n.T("This episode timed out transcoding; try again"))
		}
		log.Printf("codie ffmpeg: %s", tailLine([]byte(said.String())))
		return codieSegment{}, fmt.Errorf("%s", tailLine([]byte(said.String())))
	}
	return codieSegment{path: dst, durMS: probeFileDurationMS(dst)}, nil
}

// codieSegmentArgs is ffmpeg's arguments for one segment: the window asked
// for, in the device's shape, at the pace asked for.
func codieSegmentArgs(s *video.Stream, rate, fromS, spanS float64, sub, dst string) []string {
	args := []string{"-hide_banner", "-loglevel", "error", "-nostdin", "-y"}
	hdr := ffmpegHeaders(s.Headers)
	input := func(url string) {
		if hdr != "" {
			args = append(args, "-headers", hdr)
		}
		if fromS > 0 {
			args = append(args, "-ss", strconv.FormatFloat(fromS, 'f', 3, 64))
		}
		args = append(args, "-reconnect", "1", "-reconnect_streamed", "1",
			"-reconnect_delay_max", "5", "-rw_timeout", "15000000", "-i", url)
	}
	input(s.URL)
	if s.AudioURL != "" {
		input(s.AudioURL)
		args = append(args, "-map", "0:v:0", "-map", "1:a:0?")
	} else {
		args = append(args, "-map", "0:v:0?", "-map", "0:a:0?")
	}
	if spanS > 0 {
		args = append(args, "-t", strconv.FormatFloat(spanS, 'f', 3, 64))
	}
	shape := "scale=1920:1080:force_original_aspect_ratio=decrease,pad=ceil(iw/2)*2:ceil(ih/2)*2"
	if sub != "" {
		shape += "," + subtitleFilter(sub)
	}
	if rate != 1 {
		args = append(args,
			"-c:v", "libx264", "-preset", "veryfast", "-crf", "23",
			"-vf", fmt.Sprintf("setpts=PTS/%g,", rate)+shape,
			"-c:a", "aac", "-b:a", "160k",
			"-af", fmt.Sprintf("atempo=%g", rate))
	} else {
		args = append(args,
			"-c:v", "libx264", "-preset", "veryfast", "-crf", "23",
			"-vf", shape,
			"-c:a", "aac", "-b:a", "160k")
	}
	return append(args, "-movflags", "+faststart", dst)
}

// codieProbeSeconds is how long the episode is, from what the source said, or
// from ffprobe when it didn't. Zero means nobody knows.
func (a *app) codieProbeSeconds(s *video.Stream, ep video.Episode) float64 {
	if ep.Seconds > 0 {
		return float64(ep.Seconds)
	}
	ffprobe := findFFprobe()
	if ffprobe == "" {
		return 0
	}
	args := []string{"-v", "error"}
	if hdr := ffmpegHeaders(s.Headers); hdr != "" {
		args = append(args, "-headers", hdr)
	}
	args = append(args, "-show_entries", "format=duration", "-of", "json", s.URL)
	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, ffprobe, args...)
	hideWindow(cmd)
	cmd.Env = proxyEnv()
	out, err := cmd.Output()
	if err != nil {
		log.Printf("codie probe duration: %v", err)
		return 0
	}
	var f struct {
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
	}
	if json.Unmarshal(out, &f) != nil {
		return 0
	}
	d, err := strconv.ParseFloat(f.Format.Duration, 64)
	if err != nil || d <= 0 {
		return 0
	}
	return d
}

// fileSize is how many bytes a file takes, or 0 when it can't be read.
func fileSize(path string) int64 {
	if info, err := os.Stat(path); err == nil {
		return info.Size()
	}
	return 0
}

// codieShow splits what's over the device's one-session cap into segments,
// opens a session of it fullscreen, and lets the device play it. gen says
// which player the file was made for: one stopped since is not this one.
func (a *app) codieShow(dst string, rate, resumeS float64, gen uint64) {
	segs, totalMS, err := codiePrepareSegments(dst)
	if err != nil {
		os.Remove(dst)
		a.post(func() {
			if a.codieGen != gen {
				return
			}
			a.codieBusy = ""
			a.playerErr = videoProblem(err, i18n.T("This episode won’t play"))
		})
		return
	}
	if len(segs) > 1 {
		log.Printf("codie play: split oversized video into %d segments (total %dms)", len(segs), totalMS)
	}
	startMS := codieResumeStart(resumeS, rate)
	segIdx, innerStartMS := codieSegmentForTime(segs, startMS)
	req := codieout.MediaSessionRequest{
		Layout:         codieout.FullscreenLayout(),
		Playback:       &codieout.MediaPlayback{Volume: a.vol / 100},
		ConflictPolicy: "replace",
	}
	if innerStartMS > 0 {
		req.Playback.StartPositionMS = innerStartMS
	}
	if want := a.slotChoice; want > 0 && want != a.deviceSlot && a.codie != nil {
		if err := a.codie.UseSlot(want); err != nil {
			log.Printf("codie play: switch slot %d: %v", want, err)
		} else {
			a.post(func() { a.deviceSlot = want })
		}
	}
	session, err := codieOpenSessionFn(a.codie, segs[segIdx].path, req)
	log.Printf("codie play: seg=%d/%d session=%q err=%v", segIdx+1, len(segs), session, err)
	a.post(func() {
		if a.codieGen != gen {
			if session != "" && a.codie != nil {
				go a.codie.CloseMediaSession(session)
			}
			removeCodieSegments(segs)
			return
		}
		a.codieBusy = ""
		if err != nil {
			removeCodieSegments(segs)
			a.playerErr = videoProblem(err, i18n.T("The device didn’t take this episode"))
			return
		}
		a.codieSegs = segs
		a.codieSegAt = segIdx
		a.codieTotalMS = totalMS
		a.codieEpisodeMS = totalMS
		a.codieSession = session
		a.codieFile = segs[segIdx].path
		// The speed moved on while the file was making; the file is at the
		// old pace, so the episode starts over at the new one.
		if a.rate != rate {
			go a.codieRespeed(rate, a.rate)
			return
		}
		a.codiePoll(session)
		a.codiePrepareNext()
	})
}

// removeCodieSegments deletes all temporary segment files in the background.
func removeCodieSegments(segs []codieSegment) {
	go func() {
		for _, seg := range segs {
			if seg.path != "" {
				os.Remove(seg.path)
			}
		}
	}()
}

// codieSegmentForTime maps a timestamp in the whole episode (in milliseconds)
// to the segment index that holds it and the offset within that segment.
func codieSegmentForTime(segs []codieSegment, targetMS int64) (int, int64) {
	if len(segs) <= 1 || targetMS <= 0 {
		return 0, max(0, targetMS)
	}
	for i, seg := range segs {
		if seg.durMS > 0 && targetMS < seg.startMS+seg.durMS {
			return i, max(0, targetMS-seg.startMS)
		}
	}
	last := len(segs) - 1
	return last, max(0, targetMS-segs[last].startMS)
}

// codiePrepareSegments checks whether dst fits under the device's 128MiB cap;
// if it exceeds the cap, it splits dst into sequential MP4 segments that each
// fit under the cap and removes the oversized original file.
func codiePrepareSegments(dst string) ([]codieSegment, int64, error) {
	info, err := os.Stat(dst)
	if err != nil {
		return nil, 0, err
	}
	totalMS := probeFileDurationMS(dst)
	if info.Size() <= codieMaxFileBytes {
		return []codieSegment{{path: dst, startMS: 0, durMS: totalMS}}, totalMS, nil
	}
	paths, err := codieSplitOversizedMP4(dst, info.Size(), totalMS)
	if err != nil {
		return nil, 0, err
	}
	os.Remove(dst)
	var segs []codieSegment
	var cumMS int64
	fallbackDur := int64(0)
	if totalMS > 0 && len(paths) > 0 {
		fallbackDur = totalMS / int64(len(paths))
	}
	for _, p := range paths {
		dMS := probeFileDurationMS(p)
		if dMS <= 0 {
			dMS = fallbackDur
		}
		segs = append(segs, codieSegment{path: p, startMS: cumMS, durMS: dMS})
		cumMS += dMS
	}
	if cumMS <= 0 {
		cumMS = totalMS
	}
	return segs, cumMS, nil
}

// codieSplitOversizedMP4 splits srcPath into multiple MP4 files, each at or
// below codieMaxFileBytes.
func codieSplitOversizedMP4(srcPath string, sizeBytes, totalMS int64) ([]string, error) {
	ffmpeg := findFFmpegFn()
	if ffmpeg == "" {
		return nil, errors.New(i18n.T("The device needs ffmpeg to take episodes; install it with your package manager and try again"))
	}
	parts := int((sizeBytes + codieTargetSegmentBytes - 1) / codieTargetSegmentBytes)
	if parts < 2 {
		parts = 2
	}
	segSec := 45
	if totalMS > 0 {
		segSec = max(10, int(float64(totalMS)/1000.0/float64(parts)))
	}
	paths, err := codieFFmpegSegment(ffmpeg, srcPath, segSec, parts, totalMS)
	if err != nil {
		return nil, err
	}
	// Verify every piece fits under codieMaxFileBytes; if any piece still
	// exceeds the cap (e.g. a bitrate spike), split that piece further.
	var finalPaths []string
	for _, p := range paths {
		pInfo, err := os.Stat(p)
		if err != nil {
			continue
		}
		if pInfo.Size() <= codieMaxFileBytes || segSec <= 5 {
			finalPaths = append(finalPaths, p)
			continue
		}
		subDurMS := probeFileDurationMS(p)
		subPaths, subErr := codieFFmpegSegment(ffmpeg, p, max(5, segSec/2), 2, subDurMS)
		os.Remove(p)
		if subErr != nil {
			for _, fp := range finalPaths {
				os.Remove(fp)
			}
			return nil, subErr
		}
		finalPaths = append(finalPaths, subPaths...)
	}
	if len(finalPaths) == 0 {
		return nil, errors.New(i18n.T("This episode won’t play"))
	}
	return finalPaths, nil
}

// codieFFmpegSegment splits srcPath with ffmpeg's segment muxer (-c copy),
// falling back to explicit time-slice copies when the stream has too few
// keyframes for the segment muxer to divide it.
func codieFFmpegSegment(ffmpeg, srcPath string, segSec, parts int, totalMS int64) ([]string, error) {
	base := strings.TrimSuffix(srcPath, ".mp4")
	pattern := base + "-seg-%03d.mp4"
	globPattern := base + "-seg-*.mp4"
	cmd := exec.Command(ffmpeg,
		"-hide_banner", "-loglevel", "error", "-nostdin", "-y",
		"-i", srcPath,
		"-c", "copy", "-map", "0",
		"-f", "segment",
		"-segment_time", strconv.Itoa(segSec),
		"-reset_timestamps", "1",
		"-segment_format_options", "movflags=+faststart",
		pattern,
	)
	hideWindow(cmd)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("ffmpeg segment: %v (%s)", err, tailLine(out))
	}
	matches, _ := filepath.Glob(globPattern)
	sort.Strings(matches)
	if len(matches) > 1 {
		return matches, nil
	}
	// Fallback when -f segment produced a single file (no keyframe boundary):
	// slice by time window if totalMS is known.
	if len(matches) == 1 {
		if info, err := os.Stat(matches[0]); err == nil && info.Size() <= codieMaxFileBytes {
			return matches, nil
		}
		os.Remove(matches[0])
	}
	if totalMS <= 0 || parts < 2 {
		return nil, errors.New("unable to split oversized video")
	}
	sliceSec := max(5, int((totalMS/1000+int64(parts)-1)/int64(parts)))
	var sliced []string
	for i := 0; i < parts; i++ {
		startSec := i * sliceSec
		outPath := fmt.Sprintf("%s-slice-%03d.mp4", base, i)
		scmd := exec.Command(ffmpeg,
			"-hide_banner", "-loglevel", "error", "-nostdin", "-y",
			"-ss", strconv.Itoa(startSec),
			"-t", strconv.Itoa(sliceSec),
			"-i", srcPath,
			"-c:v", "libx264", "-preset", "veryfast", "-crf", "23",
			"-c:a", "aac", "-b:a", "160k",
			"-movflags", "+faststart",
			outPath,
		)
		hideWindow(scmd)
		if out, err := scmd.CombinedOutput(); err != nil {
			for _, p := range sliced {
				os.Remove(p)
			}
			return nil, fmt.Errorf("ffmpeg slice: %v (%s)", err, tailLine(out))
		}
		sliced = append(sliced, outPath)
	}
	return sliced, nil
}

// probeFileDurationMS asks ffprobe for a local file's duration in milliseconds.
func probeFileDurationMS(path string) int64 {
	ffprobe := findFFprobe()
	if ffprobe == "" {
		return 0
	}
	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, ffprobe,
		"-v", "error",
		"-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1",
		path,
	).Output()
	if err != nil {
		return 0
	}
	sec, err := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	if err != nil || sec <= 0 {
		return 0
	}
	return int64(sec * 1000)
}

// codieResumeStart is where in a session's file, in milliseconds, the story
// at resumeS sits: at rate the file runs rate times faster than the story
// does. A story barely begun isn't worth resuming.
func codieResumeStart(resumeS, rate float64) int64 {
	if resumeS <= 10 || rate <= 0 {
		return 0
	}
	return int64(resumeS / rate * 1000)
}

// codieMakeMP4 turns what the source gave into one MP4 of the shape the
// device takes: H.264 with AAC, no wider than 1080p, its headers on every
// input, two inputs when the picture and the sound come apart, at rate
// times the usual speed. Streams that are already in that shape (and asked
// for at the usual speed) go through a plain copy, which takes seconds; the
// rest are transcoded, which takes minutes — a copied stream can't change
// its pace, so any other rate makes a full transcode of it.
func (a *app) codieMakeMP4(parent context.Context, s *video.Stream, rate float64) (string, error) {
	f, err := os.CreateTemp("", "codie-codie-*.mp4")
	if err != nil {
		return "", err
	}
	dst := f.Name()
	f.Close()
	if err := a.codieMakeMP4To(parent, s, rate, dst); err != nil {
		os.Remove(dst)
		return "", err
	}
	return dst, nil
}

// codieMakeMP4To makes the episode's MP4 at dst.
func (a *app) codieMakeMP4To(parent context.Context, s *video.Stream, rate float64, dst string) error {
	ffmpeg := findFFmpegFn()
	if ffmpeg == "" {
		return errors.New(i18n.T("The device needs ffmpeg to take episodes; install it with your package manager and try again"))
	}
	asIs := rate == 1 && probePlayableAsIs(parent, s, s.Headers)
	if asIs {
		log.Printf("codie play: streams already in device shape; copying")
	} else {
		log.Printf("codie play: streams need transcoding (rate %g)", rate)
	}
	args := codieFFmpegArgs(s, rate, asIs, dst)
	ctx, cancel := context.WithTimeout(parent, codieFFmpegTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, ffmpeg, args...)
	hideWindow(cmd)
	cmd.Env = proxyEnv()
	out, err := cmd.StdoutPipe()
	if err != nil {
		os.Remove(dst)
		return err
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		os.Remove(dst)
		return err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	reader := make([]byte, 0, 0)
	buf := make([]byte, 4<<10)
readLoop:
	for {
		select {
		case err := <-done:
			if err != nil {
				os.Remove(dst)
				if ctx.Err() != nil {
					return errors.New(i18n.T("This episode timed out transcoding; try again"))
				}
				return fmt.Errorf("%s", tailLine(append(reader, buf...)))
			}
			break readLoop
		default:
			n, rerr := out.Read(buf)
			if n > 0 {
				reader = append(reader, buf[:n]...)
				for _, line := range strings.Split(string(buf[:n]), "\n") {
					// ffmpeg's own complaints share the pipe with its progress
					// counters; without them a pass that goes nowhere says nothing.
					if line = strings.TrimSpace(line); line != "" && !strings.Contains(line, "=") {
						log.Printf("codie ffmpeg: %s", line)
					}
				}
			}
			if rerr != nil {
				// 输出读完了:ffmpeg 已退出,成败看它的退出码。
				if werr := <-done; werr != nil {
					os.Remove(dst)
					if ctx.Err() != nil {
						return errors.New(i18n.T("This episode timed out transcoding; try again"))
					}
					return fmt.Errorf("%s", tailLine(reader))
				}
				break readLoop
			}
		}
	}
	return nil
}

// codieFFmpegArgs is what ffmpeg gets for an episode: the inputs (the
// source's headers on each, two when picture and sound come apart), the
// maps, and the device's shape. rate bakes the pace in — setpts for the
// picture, atempo for the sound, whose range covers the whole rate menu in
// one go. asIs says the stream already plays as the device takes it, which
// only ever goes with the usual speed.
func codieFFmpegArgs(s *video.Stream, rate float64, asIs bool, dst string) []string {
	args := []string{"-hide_banner", "-loglevel", "error", "-nostdin", "-y"}
	hdr := ffmpegHeaders(s.Headers)
	input := func(url string) {
		if hdr != "" {
			args = append(args, "-headers", hdr)
		}
		// A CDN read that stalls would hang the whole pass otherwise.
		args = append(args, "-reconnect", "1", "-reconnect_streamed", "1",
			"-reconnect_delay_max", "5", "-rw_timeout", "15000000")
		args = append(args, "-i", url)
	}
	input(s.URL)
	if s.AudioURL != "" {
		input(s.AudioURL)
		args = append(args, "-map", "0:v:0", "-map", "1:a:0?")
	} else {
		args = append(args, "-map", "0:v:0?", "-map", "0:a:0?")
	}
	switch {
	case rate != 1:
		args = append(args,
			"-c:v", "libx264", "-preset", "veryfast", "-crf", "23",
			"-vf", fmt.Sprintf("setpts=PTS/%g,scale=1920:1080:force_original_aspect_ratio=decrease,pad=ceil(iw/2)*2:ceil(ih/2)*2", rate),
			"-c:a", "aac", "-b:a", "160k",
			"-af", fmt.Sprintf("atempo=%g", rate))
	case asIs:
		args = append(args, "-c", "copy")
	default:
		args = append(args,
			"-c:v", "libx264", "-preset", "veryfast", "-crf", "23",
			"-vf", "scale=1920:1080:force_original_aspect_ratio=decrease,pad=ceil(iw/2)*2:ceil(ih/2)*2",
			"-c:a", "aac", "-b:a", "160k")
	}
	return append(args, "-movflags", "+faststart", "-progress", "pipe:1", dst)
}

// codiePrepared is an episode already made for the device: the playlist's
// next video's first episode, kept until its turn comes.
type codiePrepared struct {
	source string
	item   string
	ep     string
	rate   float64
	path   string
}

// takeCodiePrepared hands back the file readied for item's episode, if it is
// the one and was made at the rate it would play at now; the file's
// ownership goes with it. Anything else leaves the file where it is.
func (a *app) takeCodiePrepared(item video.Item, ep video.Episode) (string, float64, bool) {
	p := a.prepared
	if p == nil || p.item != item.ID || p.ep != ep.ID || p.rate != a.rate {
		return "", 0, false
	}
	if p.source != "" && item.Source != "" && p.source != item.Source {
		return "", 0, false
	}
	a.prepared = nil
	return p.path, p.rate, true
}

// codiePrepareNext makes the playlist's next video's first episode for the
// device while this one plays, so moving on costs no wait. One at a time: a
// file readied for another turn would be dropped the moment this runs again.
func (a *app) codiePrepareNext() {
	if a.precoding || a.codie == nil || !a.deviceOutput || a.settings == nil {
		return
	}
	idx := a.queueAt + 1
	if a.queueAt < 0 || idx >= len(a.queue) {
		return
	}
	it := a.queue[idx]
	if it.ID == "" {
		return
	}
	ep := firstEpisodeOf(it)
	rate := a.rate
	src := a.sourceOf(it.Source)
	if src == nil {
		src = a.sourceEntry
	}
	if src == nil {
		return
	}
	if p := a.prepared; p != nil {
		if p.item == it.ID && p.ep == ep.ID && p.rate == rate && (p.source == "" || it.Source == "" || p.source == it.Source) {
			return // already readied
		}
		os.Remove(p.path)
		a.prepared = nil
	}
	a.precoding = true
	go func() {
		var path string
		err := func() error {
			f, err := os.CreateTemp("", "codie-next-*.mp4")
			if err != nil {
				return err
			}
			path = f.Name()
			f.Close()
			s, err := src.src.Play(it.ID, ep.ID)
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(context.Background(), codieFFmpegTimeout)
			defer cancel()
			return a.codieMakeMP4To(ctx, s, rate, path)
		}()
		a.post(func() {
			a.precoding = false
			if err != nil {
				if path != "" {
					os.Remove(path)
				}
				log.Printf("codie prepare next: %v", err)
				return
			}
			a.prepared = &codiePrepared{source: it.Source, item: it.ID, ep: ep.ID, rate: rate, path: path}
			log.Printf("codie prepare next: %s / %s is ready", it.ID, ep.ID)
		})
	}()
}

// cleanCodiePrepared drops files an earlier run readied and never played.
func cleanCodiePrepared() {
	matches, _ := filepath.Glob(filepath.Join(os.TempDir(), "codie-next-*.mp4"))
	for _, m := range matches {
		os.Remove(m)
	}
}

// probePlayableAsIs asks ffprobe whether the stream already plays as the
// device takes it: H.264 with AAC, no bigger than 1080p. The stream's own
// headers go with every ask, for the CDN turns down strangers.
func probePlayableAsIs(parent context.Context, s *video.Stream, headers map[string]string) bool {
	ffprobe := findFFprobe()
	if ffprobe == "" {
		return false // 不知道,就当要转
	}
	videoArgs := []string{"-v", "error"}
	if hdr := ffmpegHeaders(headers); hdr != "" {
		videoArgs = append(videoArgs, "-headers", hdr)
	}
	// 画面:要 H.264,不大于 1080p。
	vargs := append(videoArgs, "-select_streams", "v:0", "-show_entries", "stream=codec_name,width,height", "-of", "json")
	vctx, vcancel := context.WithTimeout(parent, probeTimeout)
	defer vcancel()
	vprobe := exec.CommandContext(vctx, ffprobe, append(vargs, s.URL)...)
	hideWindow(vprobe)
	vprobe.Env = proxyEnv()
	out, err := vprobe.Output()
	if err != nil {
		log.Printf("codie probe-as-is: ffprobe failed: %v, out: %s", err, string(out))
		return false
	}
	var v struct {
		Streams []struct {
			CodecName string `json:"codec_name"`
			Width     int    `json:"width"`
			Height    int    `json:"height"`
		} `json:"streams"`
	}
	if json.Unmarshal(out, &v) != nil || len(v.Streams) == 0 {
		return false
	}
	st := v.Streams[0]
	log.Printf("codie probe-as-is: video codec=%s %dx%d", st.CodecName, st.Width, st.Height)
	if st.CodecName != "h264" || st.Width > 1920 || st.Height > 1080 {
		return false
	}
	// 声音:要 AAC。画面和声音分开的源才有这一问。
	if s.AudioURL != "" {
		aargs := append(videoArgs, "-select_streams", "a:0", "-show_entries", "stream=codec_name", "-of", "json")
		actx, acancel := context.WithTimeout(parent, probeTimeout)
		defer acancel()
		aprobe := exec.CommandContext(actx, ffprobe, append(aargs, s.AudioURL)...)
		hideWindow(aprobe)
		aprobe.Env = proxyEnv()
		aout, err := aprobe.Output()
		if err != nil {
			log.Printf("codie probe-as-is: audio ffprobe failed: %v", err)
			return false
		}
		var a struct {
			Streams []struct {
				CodecName string `json:"codec_name"`
			} `json:"streams"`
		}
		if json.Unmarshal(aout, &a) != nil || len(a.Streams) == 0 || a.Streams[0].CodecName != "aac" {
			log.Printf("codie probe-as-is: audio isn't aac")
			return false
		}
	}
	return true
}

// findFFprobe finds ffprobe beside ffmpeg.
// findFFprobe finds ffprobe: the one the app ships, then one on PATH, then
// the usual places. Without it a stream can't be asked what shape it is,
// and every episode is transcoded rather than copied.
func findFFprobe() string {
	if p := bundledProgram("ffprobe"); p != "" {
		return p
	}
	if p, err := exec.LookPath("ffprobe"); err == nil {
		return p
	}
	for _, p := range []string{"/opt/homebrew/bin/ffprobe", "/usr/local/bin/ffprobe"} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// codiePoll asks the device what the session is doing, until it's over.
// Near a segment's end it asks faster: the seam that swaps the next segment
// in without a black beat is timed by these answers.
func (a *app) codiePoll(sessionID string) {
	go func() {
		gap := codiePollEvery
		for {
			time.Sleep(gap)
			// 先看页面:窗口一关(测试里没有窗口),别的不必再看。
			if a.page != pagePlay || a.codie == nil || a.codieSession != sessionID {
				return
			}
			st, err := a.codie.MediaSessionState(sessionID)
			if err != nil {
				log.Printf("codie poll: %v", err)
				// A session the device no longer knows is one that has ended:
				// the next segment, or the next episode, goes on from here.
				a.post(func() {
					if a.codieSession == sessionID {
						a.codieSession = ""
						a.codieState = nil
						a.codieAdvance()
					}
				})
				return
			} else {
				a.post(func() {
					if a.codieSession == sessionID {
						a.codieState = st
						a.codieSeamCheck(st)
						a.codieEnded()
					}
				})
			}
			gap = codiePollEvery
			if st.DurationMS > 0 && st.DurationMS-st.PositionMS < int64(20*time.Second/time.Millisecond) {
				gap = codiePollFast
			}
		}
	}()
}

// codieToggle flips between playing here and playing on the device; what
// plays now stops, and the episode starts again at the other one.
func (a *app) codieToggle(onDevice bool) {
	if a.deviceOutput == onDevice {
		return
	}
	p := a.playing
	if p.item.ID == "" {
		log.Printf("codie toggle: nothing playing yet")
		a.deviceOutput = onDevice
		return
	}
	if onDevice && a.codie == nil {
		log.Printf("codie toggle: no device paired, staying local")
		a.deviceOutput = false
		return
	}
	a.recordPlayProgress()
	a.deviceOutput = onDevice
	log.Printf("codie toggle: onDevice=%v for %s/%s", onDevice, p.item.Source, p.item.ID)
	a.playEpisode(p.item, p.ep, p.at)
}

// codieSlotCount is how many character slots the device has (1..5).
const codieSlotCount = 5

// syncDeviceSlot asks the paired device which character slot it currently
// stands in, so the detail page's slot selector starts there.
func (a *app) syncDeviceSlot() {
	if a.slotChoice <= 0 {
		a.slotChoice = 1
	}
	c := a.codie
	if c == nil {
		return
	}
	go func() {
		slot, err := c.ActiveSlot()
		a.post(func() {
			if err != nil {
				log.Printf("device slot: %v", err)
				return
			}
			wasDevice := a.deviceSlot
			a.deviceSlot = slot
			if a.slotChoice <= 0 || a.slotChoice == wasDevice {
				a.slotChoice = slot
			}
		})
	}()
}

// selectSlot picks character slot want (1..5) right on the detail page and
// switches the paired device to it in the background.
func (a *app) selectSlot(want int) {
	if want < 1 || want > codieSlotCount {
		return
	}
	a.slotChoice = want
	a.slotErr = ""
	c := a.codie
	if c == nil || want == a.deviceSlot {
		return
	}
	a.slotBusy = true
	go func() {
		err := c.UseSlot(want)
		a.post(func() {
			a.slotBusy = false
			if err != nil {
				log.Printf("device slot %d: %v", want, err)
				a.slotErr = videoProblem(err, i18n.T("Couldn't switch the device's slot"))
				return
			}
			a.deviceSlot = want
		})
	}()
}

// startPlayOnCodie starts playing an episode on the paired codie device right
// away, without a confirmation modal.
func (a *app) startPlayOnCodie(item video.Item, ep video.Episode, at int) {
	if a.codie == nil {
		return
	}
	a.deviceOutput = true
	a.playEpisode(item, ep, at)
}

// codieSwitchWindow is how long before a segment's end the next one's upload
// starts: the measured upload time plus a margin, so the new session lands
// roughly as the old one runs out — early by no more than the margin, late
// by no more than the guess is wrong.
func (a *app) codieSwitchWindow() time.Duration {
	base := a.codieUploadEMA
	if base <= 0 {
		base = float64(codieUploadSeed / time.Millisecond)
	}
	w := time.Duration(base)*time.Millisecond + codieSeamMargin
	if w < 2*time.Second {
		w = 2 * time.Second
	}
	return w
}

// codieUploadTook folds one measured upload time into the smooth one the
// next seam starts from.
func (a *app) codieUploadTook(d time.Duration) {
	ms := float64(d / time.Millisecond)
	if a.codieUploadEMA <= 0 {
		a.codieUploadEMA = ms
		return
	}
	a.codieUploadEMA = a.codieUploadEMA/2 + ms/2
}

// codieSeamCheck is the seam: while a segment plays and the next is within
// reach of the window, the next session's upload starts before this one is
// out, and the device swaps the moment the file lands — the picture goes on
// without a black beat between the parts. When the next file isn't made yet,
// it only lights the stage's loading line, which beats a silent screen.
func (a *app) codieSeamCheck(st *codieout.MediaSessionState) {
	if a.codieSwitchingSeg || st == nil || !st.IsPlaying || st.DurationMS <= 0 {
		return
	}
	remain := time.Duration(st.DurationMS-st.PositionMS) * time.Millisecond
	if remain > a.codieSwitchWindow() {
		return
	}
	if a.codieSegAt+1 >= len(a.codieSegs) {
		a.codieSeam = true // 下一段还在生成:先亮个加载,别黑着不说
		return
	}
	a.codieSeam = true
	a.codieSwitchingSeg = true
	next := a.codieSegAt + 1
	gen := a.codieGen
	if a.win == nil {
		a.codieSwitchSegment(gen, next, 0)
		return
	}
	go a.codieSwitchSegment(gen, next, 0)
}

// codieEnded advances to the next segment when an oversized episode was split
// into multiple files, or latches the next-episode (or next playlist item)
// countdown once the last segment has played to its end.
func (a *app) codieEnded() {
	st := a.codieState
	if st == nil || st.IsPlaying || st.DurationMS <= 0 || st.PositionMS < st.DurationMS-1500 {
		return
	}
	if a.codieSegAt >= 0 && a.codieSegAt < len(a.codieSegs) && st.DurationMS > 0 {
		a.codieUpdateSegmentDuration(a.codieSegAt, st.DurationMS)
	}
	a.codieAdvance()
}

// codieAdvance goes on from the segment that's over: to the next segment, to
// waiting for it while it's still being made, or to the next episode's
// countdown when there is nothing after this.
func (a *app) codieAdvance() {
	if a.codieSegAt+1 < len(a.codieSegs) {
		if !a.codieSwitchingSeg {
			a.codieSwitchingSeg = true
			nextSeg := a.codieSegAt + 1
			gen := a.codieGen
			go a.codieSwitchSegment(gen, nextSeg, 0)
		}
		return
	}
	if a.codieMore {
		// The next segment is still being made: hold on to this one's last
		// frame and ask again shortly, rather than counting the next episode in.
		if !a.codieSwitchingSeg {
			a.codieSwitchingSeg = true
			gen := a.codieGen
			time.AfterFunc(1500*time.Millisecond, func() {
				a.post(func() {
					if a.codieGen != gen {
						return
					}
					a.codieSwitchingSeg = false
					a.codieEnded()
				})
			})
		}
		return
	}
	if !a.nextLatch {
		a.nextLatch = true
		a.codieSeam = false // 这一集到头了:倒计时接手,加载的话到此为止
		a.startNextCountdown()
		if a.win != nil {
			a.win.Invalidate()
		}
	}
}

// codieUpdateSegmentDuration refines segment idx's duration from what the
// device reported and recomputes subsequent segments' start offsets.
func (a *app) codieUpdateSegmentDuration(idx int, durMS int64) {
	if idx < 0 || idx >= len(a.codieSegs) || durMS <= 0 || a.codieSegs[idx].durMS == durMS {
		return
	}
	a.codieSegs[idx].durMS = durMS
	var cum int64
	for i := range a.codieSegs {
		a.codieSegs[i].startMS = cum
		cum += a.codieSegs[i].durMS
	}
	if cum > 0 {
		a.codieTotalMS = cum
	}
}

// codieSwitchSegment opens segment segIdx on the device (replacing the
// previous segment's session) so a multi-segment episode plays continuously.
func (a *app) codieSwitchSegment(gen uint64, segIdx int, innerStartMS int64) {
	var seg codieSegment
	var total int
	var vol float64
	c := a.codie
	if c == nil {
		return
	}
	if segIdx < 0 || segIdx >= len(a.codieSegs) {
		return
	}
	seg = a.codieSegs[segIdx]
	total = len(a.codieSegs)
	vol = a.vol
	req := codieout.MediaSessionRequest{
		Layout:         codieout.FullscreenLayout(),
		Playback:       &codieout.MediaPlayback{Volume: vol / 100},
		ConflictPolicy: "replace",
	}
	if innerStartMS > 0 {
		req.Playback.StartPositionMS = innerStartMS
	}
	t0 := time.Now()
	session, err := codieOpenSessionFn(c, seg.path, req)
	took := time.Since(t0)
	log.Printf("codie play: switch seg=%d/%d session=%q err=%v (upload %v)", segIdx+1, total, session, err, took)
	apply := func() {
		if a.codieGen != gen {
			if session != "" && a.codie != nil {
				go a.codie.CloseMediaSession(session)
			}
			return
		}
		a.codieSwitchingSeg = false
		if err != nil {
			a.codieSeam = false
			a.playerErr = videoProblem(err, i18n.T("The device didn’t take this episode"))
			return
		}
		a.codieUploadTook(took)
		// 换段上传的这几秒里要是有人按了暂停,新会话也得停着,不能自己放起来。
		// 播完触发的换段不算:那不是暂停,是这一段放完了。
		held := a.codieState != nil && !a.codieState.IsPlaying &&
			a.codieState.DurationMS > 0 && a.codieState.PositionMS < a.codieState.DurationMS-1500
		a.codieSegAt = segIdx
		a.codieSession = session
		a.codieFile = seg.path
		a.codieSeam = false
		a.codieState = &codieout.MediaSessionState{
			SessionID:  session,
			PositionMS: innerStartMS,
			DurationMS: seg.durMS,
			IsPlaying:  !held,
		}
		if held {
			codieMediaAction(a.codie, session, "pause", nil)
		}
		a.codiePoll(session)
	}
	if a.win == nil {
		apply()
		return
	}
	a.post(apply)
}

// codieRespeed plays the episode on the device again at the rate it moved
// to: the file is made anew, for a stream that copies can't change its
// pace, and the session picks up where the story was.
func (a *app) codieRespeed(oldRate, newRate float64) {
	item, ep, at := a.playing.item, a.playing.ep, a.playing.at
	if item.ID == "" || a.codie == nil {
		return
	}
	pos, _ := a.codieTimes()
	contentS := pos.Seconds() * oldRate
	a.stopPlayerForCodie()
	go func() {
		src := a.sourceOf(item.Source)
		if src == nil {
			src = a.sourceEntry
		}
		if src == nil {
			return
		}
		s, err := src.src.Play(item.ID, ep.ID)
		if err != nil {
			a.post(func() {
				a.codieBusy = ""
				a.playerErr = videoProblem(err, i18n.T("This episode won’t play"))
			})
			return
		}
		a.playEpisodeOnDevice(item, ep, at, s, contentS/newRate, func() (*video.Stream, error) {
			return src.src.Play(item.ID, ep.ID)
		})
	}()
}

// codiePlayPause pauses or resumes the session on the device.
func (a *app) codiePlayPause() {
	if a.codie == nil || a.codieSession == "" {
		return
	}
	action := "pause"
	if a.codieState != nil && !a.codieState.IsPlaying {
		action = "resume"
	}
	codieMediaAction(a.codie, a.codieSession, action, nil)
}

// codieSeek seeks the session on the device, switching segments automatically
// when the target timestamp lands in another segment of a split episode.
func (a *app) codieSeek(ms int64) {
	if a.codie == nil || a.codieSession == "" {
		return
	}
	if len(a.codieSegs) > 1 {
		segIdx, innerMS := codieSegmentForTime(a.codieSegs, ms)
		if segIdx != a.codieSegAt {
			// 拖进还没生成的段落时,停在目标段的末尾前:后面的段落还在生成,
			// seek 到空气里只会让会话不明不白地结束。
			if d := a.codieSegs[segIdx].durMS; d > 0 && innerMS > d-500 {
				innerMS = max(0, d-500)
			}
			if !a.codieSwitchingSeg {
				a.codieSwitchingSeg = true
				gen := a.codieGen
				go a.codieSwitchSegment(gen, segIdx, innerMS)
			}
			return
		}
		ms = innerMS
	}
	// 段内同理:进度条按整集画,拖出已生成的范围就停在末尾前。
	if a.codieSegAt >= 0 && a.codieSegAt < len(a.codieSegs) {
		if d := a.codieSegs[a.codieSegAt].durMS; d > 0 && ms > d-500 {
			ms = max(0, d-500)
		}
	}
	codieMediaAction(a.codie, a.codieSession, "seek", map[string]any{"position_ms": ms})
}

// codieVolume sets how loud the session plays on the device.
func (a *app) codieVolume(v float64) {
	if a.codie == nil || a.codieSession == "" {
		return
	}
	codieMediaAction(a.codie, a.codieSession, "set_volume", map[string]any{"volume": v})
}

// codieStatus is what the stage says while the device plays.
func (a *app) codieStatus() string {
	switch {
	case a.codieBusy != "":
		return a.codieBusy
	case a.codieSeam:
		return i18n.T("Continuing to the next part…")
	case a.playerErr != "":
		return a.playerErr
	case a.codieSession == "":
		return i18n.T("Sending to the device…")
	}
	return ""
}

// stopPlayerForCodie is the codie half of stopping: the session goes, and
// all segment files made for it.
func (a *app) stopPlayerForCodie() {
	a.codieGen++
	if a.codieCancel != nil {
		a.codieCancel()
		a.codieCancel = nil
	}
	a.codieSwitchingSeg = false
	a.codieSeam = false
	if session := a.codieSession; session != "" {
		a.codieSession = ""
		go func() {
			if a.codie != nil {
				a.codie.CloseMediaSession(session)
			}
		}()
	}
	segs := a.codieSegs
	a.codieSegs = nil
	a.codieSegAt = 0
	a.codieTotalMS = 0
	a.codieEpisodeMS = 0
	file := a.codieFile
	a.codieFile = ""
	if len(segs) > 0 || file != "" {
		go func() {
			for _, seg := range segs {
				if seg.path != "" {
					os.Remove(seg.path)
				}
			}
			if file != "" {
				os.Remove(file)
			}
		}()
	}
	a.codieState = nil
	a.codieBusy = ""
}
