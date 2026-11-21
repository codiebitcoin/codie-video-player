package app

import (
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"codie-video-player/internal/i18n"
)

// The app's colors, kept as one palette the windows draw with. There are two
// of them, dark as a cinema and light as daylight; which shows is the
// appearance setting, or, when it follows the system, whatever the system
// says at the moment the window draws. The palette is a package variable the
// main thread re-sets at the top of every window's content: one app draws,
// and every window follows the same appearance.

// Appearance settings.
const (
	AppearanceSystem = "system"
	AppearanceDark   = "dark"
	AppearanceLight  = "light"
)

// palette is the colors one appearance draws with.
type palette struct {
	Dark bool

	bg, text, muted, faint, hairline, hover, press ui.Color
	accent, accentDeep, onAccent, scrub            ui.Color
	panel, panelBorder, veil                       ui.Color
	stage                                          ui.Color // the picture's own surface, dark in both
}

// colors is the palette the windows draw with now.
var colors = darkPalette()

// darkPalette is the cinema's.
func darkPalette() palette {
	return palette{
		Dark:    true,
		bg:      ui.Hex("#141416"),
		text:    ui.Hex("#f5f5f7"),
		muted:   ui.RGBA(235, 235, 245, 0.56),
		faint:   ui.RGBA(235, 235, 245, 0.3),
		hairline: ui.RGBA(255, 255, 255, 0.08),
		hover:   ui.RGBA(255, 255, 255, 0.06),
		press:   ui.RGBA(255, 255, 255, 0.1),
		accent:  ui.Oklch(0.62, 0.21, 20),
		accentDeep: ui.Oklch(0.62, 0.21, 20),
		onAccent:   ui.Hex("#ffffff"),
		scrub:      ui.Oklch(0.66, 0.16, 25),
		panel:      ui.Hex("#1e1e22"),
		panelBorder: ui.RGBA(255, 255, 255, 0.08),
		veil:       ui.RGBA(0, 0, 0, 0.6),
		stage:      ui.RGB(0, 0, 0),
	}
}

// lightPalette is the daylight's, for reading a list of dramas in the sun.
func lightPalette() palette {
	return palette{
		Dark:    false,
		bg:      ui.Hex("#f5f5f7"),
		text:    ui.Hex("#1d1d1f"),
		muted:   ui.RGBA(29, 29, 31, 0.58),
		faint:   ui.RGBA(29, 29, 31, 0.32),
		hairline: ui.RGBA(0, 0, 0, 0.1),
		hover:   ui.RGBA(0, 0, 0, 0.05),
		press:   ui.RGBA(0, 0, 0, 0.08),
		accent:  ui.Oklch(0.58, 0.21, 22),
		accentDeep: ui.Oklch(0.58, 0.21, 22),
		onAccent:   ui.Hex("#ffffff"),
		scrub:      ui.Oklch(0.58, 0.18, 25),
		panel:      ui.Hex("#ffffff"),
		panelBorder: ui.RGBA(0, 0, 0, 0.12),
		veil:       ui.RGBA(0, 0, 0, 0.4),
		stage:      ui.RGB(10, 10, 12),
	}
}

// paletteFor is the palette of an appearance setting, drawn now.
func paletteFor(appearance string) palette {
	dark := true
	switch appearance {
	case AppearanceLight:
		dark = false
	case AppearanceDark:
		dark = true
	default:
		dark = mygo.Theme.IsDark()
	}
	if dark {
		return darkPalette()
	}
	return lightPalette()
}

// applyPalette makes the windows draw with the appearance's colors.
func (a *app) applyPalette() {
	colors = paletteFor(a.settings.Appearance)
}

// applyTheme is the theme every window opens with: the framework's theme is
// derived once from the system's dark mode, so its own colors are all
// replaced by the palette's, or the controls keep the system's dark faces
// under a light page.
func (a *app) applyTheme(c *ui.Context) {
	a.applyPalette()
	t := *c.Theme()
	t.Dark = colors.Dark
	t.Background = colors.bg
	t.Text = colors.text
	t.TextMuted = colors.muted
	t.Surface = colors.hover
	t.SurfaceHover = colors.press
	t.SurfacePressed = colors.press
	t.Border = colors.hairline
	t.Accent, t.AccentHover, t.AccentPressed = colors.accent, colors.accent, colors.accent
	c.SetTheme(&t)
	c.Root().Background(colors.bg)
}

// appearanceNames are the appearance settings, named for the settings rows.
var appearanceNames = []struct{ id, key string }{
	{AppearanceSystem, "Follow System"},
	{AppearanceDark, "Dark"},
	{AppearanceLight, "Light"},
}

// languageNames are the language settings, each named in itself.
func languageNames() []struct{ id, key string } {
	return []struct{ id, key string }{
		{i18n.System, "Follow System"},
		{i18n.English, "English"},
		{i18n.Chinese, "简体中文"},
	}
}
