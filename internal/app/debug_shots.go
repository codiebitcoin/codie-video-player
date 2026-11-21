package app

import (
	"os"
	"path/filepath"
	"time"

	"github.com/egoist/mygo"
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
			shot := func(w *mygo.Window, name string) {
				if w == nil {
					return
				}
				png, err := w.CapturePage()
				if err == nil {
					os.WriteFile(filepath.Join(dir, name+".png"), png, 0o644)
				}
			}
			step := func(d time.Duration, fn func()) {
				time.Sleep(d)
				mygo.RunOnMain(func() { fn(); a.win.Invalidate() })
			}
			time.Sleep(4 * time.Second)
			shot(a.win, "1-home-hello")
			step(1, func() {
				a.settings.WizardDone = true
				a.loadTab()
			})
			time.Sleep(4 * time.Second)
			shot(a.win, "1-home")
			step(1, func() {
				// The walk goes where answers come surely: the plugin source,
				// whose list is warm, over the built-in one.
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
			time.Sleep(1500 * time.Millisecond)
			mygo.RunOnMain(mygo.App.Quit)
		}()
	}
}
