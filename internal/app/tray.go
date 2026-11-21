package app

import (
	_ "embed"
	"log"
	"runtime"

	"github.com/egoist/mygo"

	"codie-video-player/internal/i18n"
)

// The app's icon in the menu bar on macOS and the notification area on
// Windows and Linux. While it sits there, closing the window leaves the app
// at hand; the icon opens the window again, and its quit ends the app for
// good — the only quit there is, short of the Dock's. The menu is built once
// at startup, in the language of that moment.

//go:embed tray/tray-template.png
var trayTemplate []byte

//go:embed tray/tray-color.png
var trayColor []byte

// makeTray puts the app's icon in the menu bar or the notification area,
// with its menu of coming back and going away.
func makeTray() {
	icon := trayColor
	template := false
	if runtime.GOOS == "darwin" {
		icon, template = trayTemplate, true
	}
	_, err := mygo.NewTray(mygo.TrayOptions{
		ID:             "codie-video",
		Icon:           icon,
		IconIsTemplate: template,
		ToolTip:        "Codie Video",
		Menu: mygo.NewMenu([]*mygo.MenuItem{
			{Label: i18n.T("Open Codie Video"), Click: func(*mygo.MenuItem, *mygo.Window) {
				if current != nil && current.win != nil {
					current.win.Show()
					current.win.Focus()
				}
			}},
			mygo.Separator(),
			{Label: i18n.T("Quit"), Click: func(*mygo.MenuItem, *mygo.Window) {
				mygo.App.Quit()
			}},
		}),
	})
	if err != nil {
		log.Printf("tray: %v", err)
	}
}
