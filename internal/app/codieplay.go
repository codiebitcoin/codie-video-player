package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
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
	

	dst, err := a.codieMakeMP4(s)
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
// input, two inputs when the picture and the sound come apart.
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
	args := []string{"-hide_banner", "-loglevel", "error", "-y"}
	hdr := ffmpegHeaders(s.Headers)
	input := func(url string) {
		if hdr != "" {
			args = append(args, "-headers", hdr)
		}
		args = append(args, "-i", url)
	}
	input(s.URL)
	if s.AudioURL != "" {
		input(s.AudioURL)
		args = append(args, "-map", "0:v:0", "-map", "1:a:0?")
	} else {
		args = append(args, "-map", "0:v:0?", "-map", "0:a:0?")
	}
	args = append(args,
		"-c:v", "libx264", "-preset", "veryfast", "-crf", "23",
		"-vf", "scale=1920:1080:force_original_aspect_ratio=decrease,pad=ceil(iw/2)*2:ceil(ih/2)*2",
		"-c:a", "aac", "-b:a", "160k",
		"-movflags", "+faststart", dst)
	ctx, cancel := context.WithTimeout(context.Background(), codieFFmpegTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, ffmpeg, args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		os.Remove(dst)
		if ctx.Err() != nil {
			return "", errors.New("这一集转码超时了,请重试")
		}
		return "", fmt.Errorf("%s", tailLine(out))
	}
	return dst, nil
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
			if err == nil {
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
