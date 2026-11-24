package app

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/egoist/mygo"

	"codie-video-player/internal/codieout"
	"codie-video-player/internal/i18n"
	"codie-video-player/internal/video"
)

// shotHook, when set, drives the app to check its design.
var shotHook func(a *app)

// shotsMode marks a run that only exists to be looked at: its data goes to a
// folder of its own, so the real viewer's history and settings stay alone.
var shotsMode bool

// With MM_SHOTS set to a directory, the app walks through its views and
// saves what the windows show there, for checking the design: the first
// page with its hello, a category, one drama's episodes, an episode playing
// with its controls and without, the drawer of episodes, a search's
// answers, the settings, and the little window.
func init() {
	dir := os.Getenv("MM_SHOTS")
	if dir == "" {
		return
	}
	shotsMode = true
	shotHook = func(a *app) {
		go func() {
			os.MkdirAll(dir, 0o755)
			shot := func(w *mygo.Window, name string) {
				if w == nil {
					log.Printf("shots: %s: no window", name)
					return
				}
				png, err := w.CapturePage()
				if err != nil {
					log.Printf("shots: %s: %v", name, err)
					return
				}
				full := filepath.Join(dir, name+".png")
				if err := os.WriteFile(full, png, 0o644); err != nil {
					log.Printf("shots: %s: write: %v", name, err)
					return
				}
				log.Printf("shots: %s -> %s (%d bytes)", name, full, len(png))
			}
			step := func(d time.Duration, fn func()) {
				time.Sleep(d)
				mygo.RunOnMain(func() { fn(); a.win.Invalidate() })
			}
			time.Sleep(4 * time.Second)
			shot(a.win, "1-home-hello")
			step(1, func() {
				// The walk reads in English, whatever the machine speaks.
				a.settings.WizardDone = true
				a.settings.Language = i18n.English
				a.setLanguage()
				a.loadTab()
			})
			time.Sleep(4 * time.Second)
			shot(a.win, "1-home")
			step(1, func() {
				// The walk goes where answers come surely: Bilibili, after the
				// default plugin (YouTube) has shown itself first.
				for i := range a.sources {
					if a.sources[i].id == "bilibili" {
						a.useSource("bilibili")
					}
				}
			})
			time.Sleep(5 * time.Second)
			shot(a.win, "2-category")
			step(1, func() {
				if len(a.items) > 0 {
					a.openDetail(a.items[0])
				}
			})
			time.Sleep(4 * time.Second)
			shot(a.win, "3-detail")
			step(1, func() {
				if d := a.detail; d != nil && len(d.Episodes) > 0 {
					a.playEpisode(d.Item, d.Episodes[0], 0)
				}
			})
			time.Sleep(14 * time.Second)
			shot(a.win, "4-playing")
			step(1, func() {
				a.controlsOn = false
			})
			time.Sleep(1200 * time.Millisecond)
			shot(a.win, "4b-playing-clean")
			step(1, func() {
				// The device's now-playing card, as it stands while the
				// picture is on the codie device's screen, alone and then
				// with its controls (the speed pill among them). A run whose
				// source gave nothing to play still shows the card, with
				// whatever the grid found.
				if a.page != pagePlay {
					it := video.Item{ID: "shot", Title: "示例短剧"}
					if len(a.items) > 0 {
						it = a.items[0]
					}
					d := &video.Video{Item: it}
					for i := 0; i < 12; i++ {
						d.Episodes = append(d.Episodes, video.Episode{ID: fmt.Sprintf("e%d", i+1)})
					}
					a.page = pagePlay
					a.playing = playing{item: it, ep: video.Episode{ID: "e1", Title: "第1集"}, at: 0}
					a.detail = d
				}
				a.deviceOutput = true
				a.codie = codieout.New(&codieout.Creds{Addr: "127.0.0.1:1"})
				a.codieState = &codieout.MediaSessionState{IsPlaying: true, PositionMS: 95_000, DurationMS: 120_000}
				a.controlsOn = false
			})
			time.Sleep(1200 * time.Millisecond)
			shot(a.win, "4c-device-clean")
			step(1, func() {
				a.controlsOn = true
			})
			time.Sleep(800 * time.Millisecond)
			shot(a.win, "4d-device-controls")
			step(1, func() {
				// Paired but playing here: the controls still carry the
				// 本机/codie toggle, so the device is one press away.
				a.controlsOn = false
				a.deviceOutput = false
				a.codieState = nil
				a.controlsOn = true
			})
			time.Sleep(800 * time.Millisecond)
			shot(a.win, "4e-local-paired")
			step(1, func() {
				a.controlsOn = false
				a.codie = nil
				a.drawerOpen = true
			})
			time.Sleep(1500 * time.Millisecond)
			shot(a.win, "5-drawer")
			step(1, func() {
				a.drawerOpen = false
				a.page = pageList
				a.query = "重生"
				a.searchAll("重生")
			})
			time.Sleep(6 * time.Second)
			shot(a.win, "6-search")
			step(1, func() {
				a.openSettingsWindow()
			})
			time.Sleep(3 * time.Second)
			shot(a.settingsWin, "7-settings")
			step(1, func() {
				if a.player != nil {
					a.openPip()
				}
			})
			time.Sleep(3 * time.Second)
			shot(a.pipWin, "8-pip")
			step(1, func() {
				a.closePip()
			})
			time.Sleep(1 * time.Second)
			step(0, func() {
				// The daylight palette, over the same home.
				a.settings.Appearance = AppearanceLight
				a.applyPalette()
				a.page = pageList
				a.rethemeWindows()
			})
			time.Sleep(3 * time.Second)
			shot(a.win, "9-light-home")
			step(1, func() {
				a.settings.Appearance = AppearanceSystem
				a.applyPalette()
				a.rethemeWindows()
			})
			time.Sleep(1500 * time.Millisecond)
			mygo.RunOnMain(mygo.App.Quit)
		}()
	}
}
