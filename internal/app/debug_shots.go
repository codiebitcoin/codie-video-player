package app

import (
	"os"
	"path/filepath"
	"time"

	"github.com/egoist/mygo"
)

// shotHook, when set, drives the app to check its design.
var shotHook func(a *app)

// With MM_SHOTS set to a directory, the app walks through its views and saves
// what the window shows there, for checking the design: the first page, a
// category of dramas, one drama's episodes, and an episode playing.
func init() {
	dir := os.Getenv("MM_SHOTS")
	if dir == "" {
		return
	}
	shotHook = func(a *app) {
		go func() {
			shot := func(name string) {
				png, err := a.win.CapturePage()
				if err == nil {
					os.WriteFile(filepath.Join(dir, name+".png"), png, 0o644)
				}
			}
			step := func(d time.Duration, fn func()) {
				time.Sleep(d)
				mygo.RunOnMain(func() { fn(); a.win.Invalidate() })
			}
			time.Sleep(4 * time.Second)
			shot("1-home")
			step(0, func() {
				for _, t := range a.tabs {
					if t.Label == "漫剧" {
						a.useTab(t.ID)
					}
				}
			})
			time.Sleep(5 * time.Second)
			shot("2-category")
			step(0, func() {
				if len(a.items) > 0 {
					a.openDetail(a.items[0])
				}
			})
			time.Sleep(4 * time.Second)
			shot("3-detail")
			step(0, func() {
				if d := a.detail; d != nil && len(d.Episodes) > 0 {
					a.playEpisode(d.Item, d.Episodes[0], 0)
				}
			})
			time.Sleep(25 * time.Second)
			shot("4-playing")
			mygo.RunOnMain(mygo.App.Quit)
		}()
	}
}
