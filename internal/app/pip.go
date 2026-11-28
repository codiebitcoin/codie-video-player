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
	if a.pipOpacity <= 0 {
		a.pipOpacity = 1
	}
	w, h := 0, 0
	x, y := 0, 0
	if a.win != nil {
		w, h = a.win.Size()
		x, y = a.win.Position()
	}
	px, py := x+w-pipWidth-24, y+h-24
	if px < x {
		px = x + 24
	}
	if py < y {
		py = y + 24
	}
	if a.pipX != 0 || a.pipY != 0 {
		px, py = a.pipX, a.pipY
	}
	pw, ph := a.pipSize()
	a.pipWin = mygo.NewWindow(mygo.WindowOptions{
		Title:             i18n.T("Codie Video — Mini"),
		Width:             pw,
		Height:            ph,
		X:                 px,
		Y:                 py,
		Opacity:           a.pipOpacity,
		TitleBarStyle:     mygo.TitleBarHidden,
		HideWindowButtons: true, // the strip's own cross closes it
		BackgroundColor:   "#000000",
		Content:           ui.View(a.pipContent),
	})
	a.pipWin.SetAlwaysOnTop(true)
	// Where the listener put it is where it stays, this run and the next.
	a.pipWin.OnMove(func() {
		if a.pipWin == nil {
			return
		}
		mx, my := a.pipWin.Position()
		a.post(func() { a.pipX, a.pipY = mx, my })
	})
	// The opacity slider's value goes to the window as it moves: a look that
	// answers the hand, not one that waits for a rebuild.
	go func() {
		applied := a.pipOpacity
		for a.pipWin != nil {
			time.Sleep(120 * time.Millisecond)
			if a.pipWin == nil || a.pipOpacity == applied {
				continue
			}
			applied = a.pipOpacity
			op := applied
			a.post(func() {
				if a.pipWin != nil {
					a.pipWin.SetOpacity(op)
				}
			})
		}
	}()
	a.pipWin.OnClose(func(*mygo.CloseEvent) {
		a.post(func() { a.pipWin = nil })
	})
	a.win.Invalidate()
}

// pipSize is the little window's width and height: the width leads, at
// pipWidth, and the height follows the video's own shape — a 16:9 episode
// and a phone's upright recording each get their own. The strip rides below.
func (a *app) pipSize() (int, int) {
	w := pipWidth
	h := w * 9 / 16
	if p := a.player; p != nil && p.Ready() {
		if vw, vh := p.Size(); vw > 0 && vh > 0 {
			h = w * vh / vw
		}
	}
	if h < 120 {
		h = 120
	}
	if h > 720 {
		h = 720
	}
	return w, h + 44
}

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
			// How see-through the window is, half to fully solid. The value
			// goes to the window off the build, from the keep-alive below.
			ui.Slider(c.Key("pip-op"), &a.pipOpacity, 0.5, 1).Width(64)
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

// setFullscreen makes the picture take the whole screen, or gives it back.
// On the device it is the session's layout: stretch over the codie's screen,
// applied to the running session at once, its shape from here on. Here it is
// this window over this screen.
func (a *app) setFullscreen(on bool) {
	if a.fullscreen == on {
		return
	}
	a.fullscreen = on
	if a.deviceOutput && a.codie != nil {
		// 设备端:立即把正在播的会话切到拉伸/原比例,之后的段照用。
		if a.codieSession != "" {
			codieMediaAction(a.codie, a.codieSession, "set_layout", map[string]any{"layout": a.codieLayout()})
		}
		a.wakeControls()
		if a.win != nil {
			a.win.Invalidate()
		}
		if a.deviceWin != nil {
			a.deviceWin.Invalidate()
		}
		return
	}
	if a.win == nil {
		return
	}
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

// rotateQuarter turns the local picture another quarter turn clockwise,
// 0 → 90 → 180 → 270 → 0. It plays as it turns.
func (a *app) rotateQuarter() {
	a.rotation = (a.rotation + 90) % 360
	a.wakeControls()
	if a.win != nil {
		a.win.Invalidate()
	}
	if a.pipWin != nil {
		a.pipWin.Invalidate()
	}
}

// playPause answers the play/pause of what plays here — or, when the
// picture is on the device, of what plays there.
func (a *app) playPause() {
	if a.deviceOutput && a.codie != nil {
		a.codiePlayPause()
		a.wakeControls()
		return
	}
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

// seekBy moves the episode on by d seconds, for the arrow keys — here or on
// the device alike.
func (a *app) seekBy(d time.Duration) {
	if a.deviceOutput && a.codie != nil {
		pos, dur := a.codieTimes()
		ms := pos.Milliseconds() + d.Milliseconds()
		if ms < 0 {
			ms = 0
		}
		if dur > 0 && ms > dur.Milliseconds()-1000 {
			ms = dur.Milliseconds() - 1000
		}
		a.codieSeek(ms)
		a.cancelNext()
		a.wakeControls()
		return
	}
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

// bumpVolume raises or lowers how loud by d, for the arrow keys — the
// device's loudness when it plays there.
func (a *app) bumpVolume(d float64) {
	v := a.vol + d
	if v < 0 {
		v = 0
	}
	if v > 100 {
		v = 100
	}
	if v == a.vol {
		return
	}
	a.setVolume(v)
	if a.deviceOutput && a.codie != nil {
		a.codieVolume(a.vol / 100)
		a.wakeControls()
		return
	}
	if p := a.player; p != nil {
		p.SetVolume(a.vol / 100)
	}
	a.wakeControls()
}
