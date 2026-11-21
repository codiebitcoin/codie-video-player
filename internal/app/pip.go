package app

import (
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"codie-video-player/internal/i18n"
)

// The little window: the episode in a small pane that floats over everything
// in the screen's corner, while the main window goes on with the browsing.
// The player is the same one the stage was showing; the stage steps aside
// for as long as the little window has it.

// pipWidth is the little window's width; its height follows the picture's
// shape plus the strip of controls under it.
const pipWidth = 424

// openPip moves what plays into the little window.
func (a *app) openPip() {
	if a.pipWin != nil || a.player == nil {
		return
	}
	if a.fullscreen {
		a.setFullscreen(false)
	}
	w, h := 0, 0
	x, y := 0, 0
	if a.win != nil {
		w, h = a.win.Size()
		x, y = a.win.Position()
	}
	px, py := x+w-pipWidth-24, y+h-pipHeight()-24
	if px < x {
		px = x + 24
	}
	if py < y {
		py = y + 24
	}
	a.pipWin = mygo.NewWindow(mygo.WindowOptions{
		Title:           i18n.T("Codie Video — Mini"),
		Width:           pipWidth,
		Height:          pipHeight(),
		X:               px,
		Y:               py,
		TitleBarStyle:      mygo.TitleBarHidden,
		HideWindowButtons:  true, // the strip's own cross closes it
		BackgroundColor:    "#000000",
		Content:            ui.View(a.pipContent),
	})
	a.pipWin.SetAlwaysOnTop(true)
	a.pipWin.OnClose(func(*mygo.CloseEvent) {
		a.post(func() { a.pipWin = nil })
	})
	a.win.Invalidate()
}

// pipHeight is the little window's height: a 16:9 picture and its strip.
func pipHeight() int { return pipWidth * 9 / 16 + 44 }

// closePip shuts the little window, back to the stage.
func (a *app) closePip() {
	if a.pipWin == nil {
		return
	}
	w := a.pipWin
	a.pipWin = nil
	w.Close()
	if a.page == pagePlay && a.win != nil {
		a.win.Show()
		a.win.Focus()
	}
}

// pipContent is what the little window shows: the picture, its name, and
// the least of the controls.
func (a *app) pipContent(c *ui.Context) {
	a.applyPalette()
	t := *c.Theme()
	t.Dark = colors.Dark
	t.Accent, t.AccentHover, t.AccentPressed = colors.accent, colors.accent, colors.accent
	c.SetTheme(&t)
	c.Root().Background(ui.RGB(0, 0, 0))

	ui.Column(c).Fill().Children(func() {
		ui.Box(c).Grow(1).MinHeight(0).Background(ui.RGB(0, 0, 0)).Children(func() {
			if p := a.player; p != nil {
				ui.Video(c, p).Fill().Fit(ui.Contain)
			}
		})
		strip := ui.Row(c).Height(44).Shrink(0).Padding(0, 10, 4, 10).Gap(8).AlignItems(ui.Center)
		strip.Background(ui.RGB(10, 10, 12))
		strip.Children(func() {
			icon := iconPlay
			if p := a.player; p != nil && p.Playing() {
				icon = iconPause
			}
			if b := iconButton(c.Key("pip-play"), icon, 15, colors.text, i18n.T("Play/Pause")); b.Clicked() {
				a.playPause()
			}
			ui.Text(c, a.playing.item.Title).FontSize(12.5).Grow(1).Shrink(1).MinWidth(0).SingleLine().TextColor(colors.muted)
			if b := iconButton(c.Key("pip-main"), iconRestore, 15, colors.muted, i18n.T("Back to Main Window")); b.Clicked() {
				a.closePip()
			}
			if b := iconButton(c.Key("pip-close"), iconX, 14, colors.muted, i18n.T("Close Mini Window")); b.Clicked() {
				a.closePip()
			}
		})
	})
}

// pipActive reports whether what plays is in the little window now.
func (a *app) pipActive() bool { return a.pipWin != nil }

// setFullscreen makes the window take the whole screen, or gives it back.
func (a *app) setFullscreen(on bool) {
	if a.fullscreen == on || a.win == nil {
		return
	}
	a.fullscreen = on
	a.win.SetFullScreen(on)
	if on {
		a.closePip()
		a.speedOpen, a.drawerOpen = false, false
	}
	a.wakeControls()
	a.win.Invalidate()
}

// toggleFullscreen flips the window between the screen and its own size.
func (a *app) toggleFullscreen() { a.setFullscreen(!a.fullscreen) }

// playPause answers the play/pause of what plays here.
func (a *app) playPause() {
	p := a.player
	if p == nil {
		return
	}
	if p.Playing() {
		p.Pause()
		a.wakeControls() // paused, the controls stay
	} else {
		p.Play()
		a.wakeControls()
	}
	a.win.Invalidate()
}

// seekBy moves the episode on by d seconds, for the arrow keys.
func (a *app) seekBy(d time.Duration) {
	p := a.player
	if p == nil || !p.Ready() || p.Err() != nil {
		return
	}
	at := p.Position() + d
	if at < 0 {
		at = 0
	}
	if dur := p.Duration(); dur > 0 && at > dur-time.Second {
		at = dur - time.Second
	}
	p.Seek(at)
	a.cancelNext()
	a.wakeControls()
}

// bumpVolume raises or lowers how loud by d, for the arrow keys.
func (a *app) bumpVolume(d float64) {
	p := a.player
	if p == nil {
		return
	}
	v := a.vol + d
	if v < 0 {
		v = 0
	}
	if v > 100 {
		v = 100
	}
	a.setVolume(v)
	p.SetVolume(a.vol / 100)
	a.wakeControls()
}
