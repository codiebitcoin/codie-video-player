package app

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"codie-video-player/internal/codieout"
	"codie-video-player/internal/video"

	"github.com/egoist/mygo"
)

// With MM_PLAY set to a directory, the app drives one full playback against
// the paired codie device by itself: the first drama of the first tab, its
// first episode, out on the device — and writes what happens, line to line,
// into that directory.
func init() {
	dir := os.Getenv("MM_PLAY")
	if dir == "" {
		return
	}
	shotHook = func(a *app) {
		go func() {
			logf := func(format string, args ...any) {
				line := time.Now().Format("15:04:05") + " " + fmt.Sprintf(format, args...) + "\n"
				f, err := os.OpenFile(filepath.Join(dir, "log.txt"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
				if err == nil {
					f.WriteString(line)
					f.Close()
				}
			}
			step := func(d time.Duration, fn func()) {
				time.Sleep(d)
				mygo.RunOnMain(func() { fn(); a.win.Invalidate() })
			}
			step(4*time.Second, func() {
				if src := os.Getenv("MM_PLAY_SOURCE"); src != "" {
					a.useSource(src)
				}
			})
			step(1*time.Second, func() {
				logf("sources: %d (%s), active: %s, grid: %d items, device paired: %v",
					len(a.sources), sourceNames(a.sources), a.sourceName(), len(a.items), a.codie != nil)
			})
			// 站点连不上时,退而测设备本身:本地生成的测试 MP4 开会话。
			fallback := func() {
				logf("site unreachable; falling back to a local test MP4")
				mp4 := "/tmp/cvp-test.mp4"
				gen := exec.Command("ffmpeg", "-y", "-f", "lavfi", "-i", "testsrc2=size=960x540:rate=24:duration=30",
					"-f", "lavfi", "-i", "sine=frequency=440:duration=30",
					"-c:v", "libx264", "-preset", "veryfast", "-pix_fmt", "yuv420p",
					"-c:a", "aac", "-shortest", mp4)
				if out, err := gen.CombinedOutput(); err != nil {
					logf("ffmpeg: %v: %s", err, tailLine(out))
					return
				}
				logf("test MP4 ready (%d bytes)", func() int64 { st, _ := os.Stat(mp4); if st == nil { return 0 }; return st.Size() }())
				a.post(func() {
					a.page = pagePlay
					a.playing = playing{item: video.Item{ID: "test", Title: "本地测试视频"}, ep: video.Episode{ID: "1", Title: "第1集"}}
					session, err := a.codie.CreateMediaSession(mp4, codieout.MediaSessionRequest{
						Layout:         codieout.FullscreenLayout(),
						Playback:       &codieout.MediaPlayback{Volume: 0.6},
						ConflictPolicy: "replace",
					})
					logf("session: %q, err: %v", session, err)
					if session != "" {
						a.codieSession = session
						a.codiePoll(session)
					}
				})
			}

			for i := 0; i < 20; i++ {
				time.Sleep(2 * time.Second)
				mygo.RunOnMain(func() {
					logf("grid: %d items, err=%q, loading=%v, tab=%q", len(a.items), a.gridErr, a.gridLoading, a.tabID)
					if len(a.items) > 0 && a.detail == nil {
						a.openDetail(a.items[0])
					}
				})
				if len(a.items) > 0 {
					break
				}
			}
			// 等详情的剧集列表,最多 20 秒。
			for i := 0; i < 10; i++ {
				time.Sleep(2 * time.Second)
				mygo.RunOnMain(func() {
					if d := a.detail; d != nil && len(d.Episodes) > 0 {
						logf("detail: %q, %d episodes", d.Title, len(d.Episodes))
					}
				})
				if d := a.detail; d != nil && len(d.Episodes) > 0 {
					break
				}
			}
			step(0, func() {
				if d := a.detail; d != nil && len(d.Episodes) > 0 {
					logf("playing locally first: %q episode %q", d.Title, d.Episodes[0].Title)
					a.playEpisode(d.Item, d.Episodes[0], 0)
				} else if len(a.items) == 0 {
					logf("no grid; falling back to a local test MP4 on the device")
					fallback()
				}
			})
			// 本机放 12 秒后,模拟用户点 [codie] 切换。
			time.Sleep(12 * time.Second)
			step(0, func() {
				logf("toggling to codie: before toggle deviceOutput=%v codie=%v item=%q",
					a.deviceOutput, a.codie != nil, a.playing.item.ID)
				a.codieToggle(true)
				logf("after toggle: deviceOutput=%v err=%q busy=%q", a.deviceOutput, a.playerErr, a.codieBusy)
			})
			for i := 1; i <= 150; i++ {
				time.Sleep(time.Second)
				logf("t=%ds session=%q busy=%q err=%q state=%+v", i, a.codieSession, a.codieBusy, a.playerErr, a.codieState)
				if a.playerErr != "" {
					logf("RESULT: error")
					break
				}
				if a.codieSession != "" && a.codieState != nil && a.codieState.IsPlaying {
					logf("RESULT: playing on the device")
					break
				}
			}
			time.Sleep(6 * time.Second)
			if png, err := a.win.CapturePage(); err == nil {
				os.WriteFile(filepath.Join(dir, "app.png"), png, 0o644)
			}
			mygo.RunOnMain(mygo.App.Quit)
		}()
	}
}
