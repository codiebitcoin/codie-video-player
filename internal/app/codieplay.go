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

// codiePollEvery is how often the window asks the device what the session is
// doing.
const codiePollEvery = 800 * time.Millisecond

// playEpisodeOnDevice makes the episode into an MP4 the device takes (splitting
// it into multiple segments if it exceeds the device's 128MiB single-session
// cap), opens a session of it fullscreen, and lets the device play it. resumeS
// is where the story was left, in seconds (0 plays from the top); the episode
// runs at whatever the app's rate is now, which is baked into the file, for the
// device itself has no say over its speed.
func (a *app) playEpisodeOnDevice(item video.Item, ep video.Episode, at int, s *video.Stream, resumeS float64) {
	rate := a.rate
	var gen uint64
	a.post(func() {
		a.stopPlayer()
		gen = a.codieGen
		a.playerErr = ""
		a.playing = playing{item: item, ep: ep, at: at}
		a.page = pagePlay
		a.codieBusy = i18n.T("Transcoding for the device…")
	})

	log.Printf("codie play: transcoding started")
	dst, err := a.codieMakeMP4(s, rate)
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
		a.codieSession = session
		a.codieFile = segs[segIdx].path
		// The speed moved on while the file was making; the file is at the
		// old pace, so the episode starts over at the new one.
		if a.rate != rate {
			go a.codieRespeed(rate, a.rate)
			return
		}
		a.codiePoll(session)
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
	out, err := exec.Command(ffprobe,
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
func (a *app) codieMakeMP4(s *video.Stream, rate float64) (string, error) {
	ffmpeg := findFFmpegFn()
	if ffmpeg == "" {
		return "", errors.New(i18n.T("The device needs ffmpeg to take episodes; install it with your package manager and try again"))
	}
	f, err := os.CreateTemp("", "codie-codie-*.mp4")
	if err != nil {
		return "", err
	}
	dst := f.Name()
	f.Close()
	asIs := rate == 1 && probePlayableAsIs(s, s.Headers)
	if asIs {
		log.Printf("codie play: streams already in device shape; copying")
	} else {
		log.Printf("codie play: streams need transcoding (rate %g)", rate)
	}
	args := codieFFmpegArgs(s, rate, asIs, dst)
	ctx, cancel := context.WithTimeout(context.Background(), codieFFmpegTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, ffmpeg, args...)
	cmd.Env = proxyEnv()
	out, err := cmd.StdoutPipe()
	if err != nil {
		os.Remove(dst)
		return "", err
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		os.Remove(dst)
		return "", err
	}
	// ffmpeg 报告自己的进度:每一行 out_time_us=微秒。
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	reader := make([]byte, 0, 0)
	buf := make([]byte, 4<<10)
	var lastUS int64 = -1
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
readLoop:
	for {
		select {
		case err := <-done:
			if err != nil {
				os.Remove(dst)
				if ctx.Err() != nil {
					return "", errors.New(i18n.T("This episode timed out transcoding; try again"))
				}
				return "", fmt.Errorf("%s", tailLine(append(reader, buf...)))
			}
			break readLoop
		case <-ticker.C:
			if lastUS >= 0 && s.Type != "" {
				a.post(func() {
					if a.codieBusy != "" {
						a.codieBusy = i18n.Tf("Transcoding for the device… %d%%", percentOf(lastUS))
					}
				})
			}
		default:
			n, rerr := out.Read(buf)
			if n > 0 {
				reader = append(reader, buf[:n]...)
				for _, line := range strings.Split(string(buf[:n]), "\n") {
					if us, ok := strings.CutPrefix(line, "out_time_us="); ok {
						if v, e := strconv.ParseInt(strings.TrimSpace(us), 10, 64); e == nil {
							lastUS = v
						}
					}
				}
			}
			if rerr != nil {
				// 输出读完了:ffmpeg 已退出,成败看它的退出码。
				if werr := <-done; werr != nil {
					os.Remove(dst)
					if ctx.Err() != nil {
						return "", errors.New(i18n.T("This episode timed out transcoding; try again"))
					}
					return "", fmt.Errorf("%s", tailLine(reader))
				}
				break readLoop
			}
		}
	}
	return dst, nil
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

// percentOf is how far a microsecond count is into a normal episode, for the
// busy line; a source that knows its duration replaces this with the real one.
func percentOf(us int64) int {
	p := int(us / 1e6 / 240 * 100) // 以 4 分钟一集估
	if p > 99 {
		p = 99
	}
	return p
}

// probePlayableAsIs asks ffprobe whether the stream already plays as the
// device takes it: H.264 with AAC, no bigger than 1080p. The stream's own
// headers go with every ask, for the CDN turns down strangers.
func probePlayableAsIs(s *video.Stream, headers map[string]string) bool {
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
	vprobe := exec.Command(ffprobe, append(vargs, s.URL)...)
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
		aprobe := exec.Command(ffprobe, append(aargs, s.AudioURL)...)
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
func findFFprobe() string {
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
func (a *app) codiePoll(sessionID string) {
	go func() {
		t := time.NewTicker(codiePollEvery)
		defer t.Stop()
		for range t.C {
			if a.codieSession != sessionID || a.page != pagePlay || a.codie == nil {
				return
			}
			st, err := a.codie.MediaSessionState(sessionID)
			if err != nil {
				log.Printf("codie poll: %v", err)
			} else {
				a.post(func() {
					if a.codieSession == sessionID {
						a.codieState = st
						a.codieEnded()
					}
				})
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
	if a.codieSegAt+1 < len(a.codieSegs) {
		if !a.codieSwitchingSeg {
			a.codieSwitchingSeg = true
			nextSeg := a.codieSegAt + 1
			gen := a.codieGen
			go a.codieSwitchSegment(gen, nextSeg, 0)
		}
		return
	}
	if !a.nextLatch {
		a.nextLatch = true
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
	session, err := codieOpenSessionFn(c, seg.path, req)
	log.Printf("codie play: switch seg=%d/%d session=%q err=%v", segIdx+1, total, session, err)
	apply := func() {
		if a.codieGen != gen {
			if session != "" && a.codie != nil {
				go a.codie.CloseMediaSession(session)
			}
			return
		}
		a.codieSwitchingSeg = false
		if err != nil {
			a.playerErr = videoProblem(err, i18n.T("The device didn’t take this episode"))
			return
		}
		a.codieSegAt = segIdx
		a.codieSession = session
		a.codieFile = seg.path
		a.codieState = &codieout.MediaSessionState{
			SessionID:  session,
			PositionMS: innerStartMS,
			DurationMS: seg.durMS,
			IsPlaying:  true,
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
		a.playEpisodeOnDevice(item, ep, at, s, contentS/newRate)
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
			if !a.codieSwitchingSeg {
				a.codieSwitchingSeg = true
				gen := a.codieGen
				go a.codieSwitchSegment(gen, segIdx, innerMS)
			}
			return
		}
		ms = innerMS
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
	a.codieSwitchingSeg = false
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
