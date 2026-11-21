package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"codie-video-player/internal/codieout"
	"codie-video-player/internal/video"
)

// 在 codie 上播放:剧集转成设备会话收的 MP4(H.264/AAC,≤1080p,≤128MiB),
// 传给设备的媒体会话,画面就落在它的屏幕上;播放、进度、音量都走会话的动作,
// 状态由轮询回来看看。

// codieMaxFileBytes is what one session file may take (the firmware's own
// cap), for the check before the upload.
const codieMaxFileBytes = 128 << 20

// codieFFmpegTimeout is how long making the episode's MP4 may take.
const codieFFmpegTimeout = 20 * time.Minute

// codiePollEvery is how often the window asks the device what the session is
// doing.
const codiePollEvery = 800 * time.Millisecond

// playEpisodeOnDevice makes the episode into an MP4 the device takes, opens
// a session of it fullscreen, and lets the device play it.
func (a *app) playEpisodeOnDevice(item video.Item, ep video.Episode, at int, s *video.Stream) {
	a.stopPlayer()
	a.playerErr = ""
	a.playing = playing{item: item, ep: ep, at: at}
	a.page = pagePlay
	a.codieBusy = "正在为设备转码…"
	

	log.Printf("codie play: transcoding started")
	dst, err := a.codieMakeMP4(s)
	log.Printf("codie play: transcode done (err=%v)", err)
	if err != nil {
		a.post(func() {
			a.codieBusy = ""
			a.playerErr = videoProblem(err, "这一集放不了")
		})
		return
	}
	if info, err := os.Stat(dst); err == nil && info.Size() > codieMaxFileBytes {
		os.Remove(dst)
		a.post(func() {
			a.codieBusy = ""
			a.playerErr = "这一集转出来超过了设备收的 128MiB,暂时放不了"
		})
		return
	}
	req := codieout.MediaSessionRequest{
		Layout:         codieout.FullscreenLayout(),
		Playback:       &codieout.MediaPlayback{Volume: a.vol / 100},
		ConflictPolicy: "replace",
	}
	session, err := a.codie.CreateMediaSession(dst, req)
	log.Printf("codie play: session=%q err=%v", session, err)
	a.post(func() {
		a.codieBusy = ""
		if err != nil {
			os.Remove(dst)
			a.playerErr = videoProblem(err, "设备没有收这一集")
			return
		}
		a.codieSession, a.codieFile = session, dst
		a.codiePoll(session)
	})
}

// codieMakeMP4 turns what the source gave into one MP4 of the shape the
// device takes: H.264 with AAC, no wider than 1080p, its headers on every
// input, two inputs when the picture and the sound come apart. Streams that
// are already in that shape go through a plain copy, which takes seconds;
// the rest are transcoded, which takes minutes.
func (a *app) codieMakeMP4(s *video.Stream) (string, error) {
	ffmpeg := findFFmpeg()
	if ffmpeg == "" {
		return "", errors.New("要装了 ffmpeg 才能把剧集转给设备:先用系统的包管理器装上,再放一次")
	}
	f, err := os.CreateTemp("", "codie-codie-*.mp4")
	if err != nil {
		return "", err
	}
	dst := f.Name()
	f.Close()
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
	if probePlayableAsIs(s, s.Headers) {
		log.Printf("codie play: streams already in device shape; copying")
		args = append(args, "-c", "copy")
	} else {
		log.Printf("codie play: streams need transcoding")
		args = append(args,
			"-c:v", "libx264", "-preset", "veryfast", "-crf", "23",
			"-vf", "scale=1920:1080:force_original_aspect_ratio=decrease,pad=ceil(iw/2)*2:ceil(ih/2)*2",
			"-c:a", "aac", "-b:a", "160k")
	}
	args = append(args, "-movflags", "+faststart", "-progress", "pipe:1", dst)
	ctx, cancel := context.WithTimeout(context.Background(), codieFFmpegTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, ffmpeg, args...)
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
					return "", errors.New("这一集转码超时了,请重试")
				}
				return "", fmt.Errorf("%s", tailLine(append(reader, buf...)))
			}
			break readLoop
		case <-ticker.C:
			if lastUS >= 0 && s.Type != "" {
				a.post(func() {
					if a.codieBusy != "" {
						a.codieBusy = fmt.Sprintf("正在为设备转码… %d%%", percentOf(lastUS))
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
						return "", errors.New("这一集转码超时了,请重试")
					}
					return "", fmt.Errorf("%s", tailLine(reader))
				}
				break readLoop
			}
		}
	}
	return dst, nil
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
	out, err := exec.Command(ffprobe, append(vargs, s.URL)...).Output()
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
		aout, err := exec.Command(ffprobe, append(aargs, s.AudioURL)...).Output()
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
	a.deviceOutput = onDevice
	p := a.playing
	if p.item.ID == "" {
		return
	}
	if onDevice && a.codie == nil {
		a.deviceOutput = false
		return
	}
	a.playEpisode(p.item, p.ep, p.at)
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
	go a.codie.MediaAction(a.codieSession, action, nil)
}

// codieSeek seeks the session on the device.
func (a *app) codieSeek(ms int64) {
	if a.codie == nil || a.codieSession == "" {
		return
	}
	go a.codie.MediaAction(a.codieSession, "seek", map[string]any{"position_ms": ms})
}

// codieVolume sets how loud the session plays on the device.
func (a *app) codieVolume(v float64) {
	if a.codie == nil || a.codieSession == "" {
		return
	}
	go a.codie.MediaAction(a.codieSession, "set_volume", map[string]any{"volume": v})
}

// codieStatus is what the stage says while the device plays.
func (a *app) codieStatus() string {
	switch {
	case a.codieBusy != "":
		return a.codieBusy
	case a.playerErr != "":
		return a.playerErr
	case a.codieSession == "":
		return "正在发送到设备…"
	}
	return ""
}

// stopPlayerForCodie is the codie half of stopping: the session goes, and
// the file made for it.
func (a *app) stopPlayerForCodie() {
	if session := a.codieSession; session != "" {
		a.codieSession = ""
		go func() {
			if a.codie != nil {
				a.codie.CloseMediaSession(session)
			}
		}()
	}
	if file := a.codieFile; file != "" {
		a.codieFile = ""
		go os.Remove(file)
	}
	a.codieState = nil
	a.codieBusy = ""
}
