package app

import (
	"log"
	"os"
	"path/filepath"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"codie-video-player/internal/i18n"
)

// The settings window: how the episode plays, what the app keeps, the
// plugins it knows, and what it is. Its own little window, so the browsing
// stays where it was.

// openSettingsWindow brings the settings window up, making it if it isn't.
func (a *app) openSettingsWindow() {
	if a.settingsWin != nil {
		a.settingsWin.Show()
		a.settingsWin.Focus()
		return
	}
	a.settingsWin = mygo.NewWindow(mygo.WindowOptions{
		Title:           i18n.T("Settings — Codie Video"),
		Width:           560,
		Height:          640,
		MinWidth:        460,
		MinHeight:       420,
		TitleBarStyle:   mygo.TitleBarHidden,
		BackgroundColor: "#141416",
		Content:         ui.View(a.settingsContent),
	})
	a.settingsWin.OnClose(func(e *mygo.CloseEvent) {
		// It goes when it closes; the next open makes it again.
		a.post(func() { a.settingsWin = nil })
	})
}

// settingsContent is what the settings window shows.
func (a *app) settingsContent(c *ui.Context) {
	a.applyPalette()
	t := *c.Theme()
	t.Dark = colors.Dark
	t.Accent, t.AccentHover, t.AccentPressed = colors.accent, colors.accent, colors.accent
	c.SetTheme(&t)
	c.Root().Background(colors.bg)

	ui.Column(c).Fill().Children(func() {
		ui.Row(c).Height(topBarHeight).Padding(12, 16, 4, 16).Gap(10).AlignItems(ui.Center).DragWindow().Children(func() {
			if b := iconButton(c.Key("settings-close"), iconX, 14, colors.muted, i18n.T("Close Settings")); b.Clicked() {
				a.settingsWin.Close()
			}
			ui.Text(c, i18n.T("Settings")).FontSize(15).FontWeight(700)
			ui.Spacer(c)
			if a.updateTag != "" {
				b := ui.ButtonBase(c.Key("update")).Height(28).PaddingX(12).Radius(999).Center().
					FocusRing(false).Label(i18n.Tf("New %s", a.updateTag)).Background(colors.accent.Alpha(0.18)).Border(1, colors.accent.Alpha(0.5))
				b.Children(func() {
					ui.Text(c, i18n.Tf("New %s", a.updateTag)).FontSize(12.5).FontWeight(600).TextColor(colors.accent)
				})
				if b.Clicked() {
					go mygo.Shell.OpenExternal(a.updateURL)
				}
			}
		})
		ui.Scroll(c.Key("settings-scroll")).Fill().Padding(10, 24, 32, 24).Children(func() {
			ui.Column(c).Fill().Gap(4).Children(func() {
				a.settingsSection(c, i18n.T("Appearance"), func() {
					a.settingsRow(c, i18n.T("Appearance"), i18n.T("Dark as a cinema, or light as daylight"), func() {
						ui.Row(c).Gap(6).Children(func() {
							for _, an := range appearanceNames {
								chosen := a.settings.Appearance == an.id
								b := ui.ButtonBase(c.Key("ap"+an.id)).Height(26).PaddingX(10).Radius(999).Center().
									FocusRing(false).Label(i18n.T(an.key))
								if chosen {
									b.Background(colors.accent).TextColor(colors.onAccent)
								} else {
									b.Background(colors.hover).TextColor(colors.text)
								}
								b.Children(func() {
									ui.Text(c, i18n.T(an.key)).FontSize(12).FontWeight(600)
								})
								if b.Clicked() {
									a.settings.Appearance = an.id
									a.settings.save()
									a.rethemeWindows()
								}
							}
						})
					})
				})
				a.settingsSection(c, i18n.T("Language"), func() {
					a.settingsRow(c, i18n.T("Language"), i18n.T("What the app's words read in"), func() {
						ui.Row(c).Gap(6).Children(func() {
							for _, ln := range languageNames() {
								chosen := a.settings.Language == ln.id
								b := ui.ButtonBase(c.Key("lang"+ln.id)).Height(26).PaddingX(10).Radius(999).Center().
									FocusRing(false).Label(ln.key)
								if chosen {
									b.Background(colors.accent).TextColor(colors.onAccent)
								} else {
									b.Background(colors.hover).TextColor(colors.text)
								}
								b.Children(func() {
									ui.Text(c, ln.key).FontSize(12).FontWeight(600)
								})
								if b.Clicked() {
									a.settings.Language = ln.id
									a.settings.save()
									a.setLanguage()
									a.rethemeWindows()
								}
							}
						})
					})
				})
				a.settingsSection(c, i18n.T("Playback"), func() {
					a.settingsRow(c, i18n.T("Auto-play Next"), i18n.T("When an episode ends, the next one starts on its own"), func() {
						on := !a.settings.NoAutoNext
						if ui.Switch(c.Key("auto-next"), &on).Changed() {
							a.settings.NoAutoNext = !on
							a.settings.save()
						}
					})
					a.settingsRow(c, i18n.T("Default Speed"), i18n.T("Every episode opens at this speed"), func() {
						rates := []float64{0.75, 1, 1.25, 1.5, 2}
						ui.Row(c).Gap(6).Children(func() {
							for _, r := range rates {
								chosen := a.rate == r
								b := ui.ButtonBase(c.Key("dr"+rateLabel(r))).Height(26).PaddingX(10).Radius(999).Center().
									FocusRing(false).Label(rateLabel(r))
								if chosen {
									b.Background(colors.accent).TextColor(colors.onAccent)
								} else {
									b.Background(colors.hover).TextColor(colors.text)
								}
								b.Children(func() {
									ui.Text(c, rateLabel(r)).FontSize(12).FontWeight(600)
								})
								if b.Clicked() {
									a.setRate(r)
								}
							}
						})
					})
					a.settingsRow(c, i18n.T("Volume"), i18n.T("Remembers where the last run left it"), func() {
						ui.Slider(c.Key("vol"), &a.vol, 0, 100).Width(160)
					})
				})
				a.settingsSection(c, i18n.T("Plugins"), func() {
					for _, p := range a.plugins {
						info := p.Info()
						on := !a.pluginDisabled(info.ID)
						id := info.ID
						a.settingsRow(c, info.Name+" v"+info.Version, id, func() {
							if ui.Switch(c.Key("p"+id), &on).Changed() {
								a.setPluginDisabled(id, !on)
							}
						})
					}
					a.settingsRow(c, i18n.T("Install Plugin"), i18n.T("A folder with a plugin.json and an index.js"), func() {
						if ui.Button(c.Key("install"), i18n.T("Choose Folder…")).Clicked() {
							a.installPluginDialog()
						}
					})
					a.settingsRow(c, i18n.T("Plugins Folder"), i18n.T("Or drop plugin folders in there yourself"), func() {
						if ui.Button(c.Key("open-dir"), i18n.T("Open")).Clicked() {
							go mygo.Shell.OpenPath(pluginsDir())
						}
					})
				})
				a.settingsSection(c, i18n.T("About"), func() {
					a.settingsRow(c, i18n.T("Version"), mygo.App.Version(), nil)
					a.settingsRow(c, i18n.T("Check for Updates"), updateLine(a.updateTag, a.updateChecking), func() {
						label := i18n.T("Check")
						if a.updateChecking {
							label = "…"
						}
						if ui.Button(c.Key("check"), label).Clicked() {
							a.checkUpdatesNow()
						}
					})
					a.settingsRow(c, i18n.T("Repository"), "github.com/codiebitcoin/codie-video-player", func() {
						if ui.Button(c.Key("repo"), i18n.T("Open")).Clicked() {
							go mygo.Shell.OpenExternal("https://github.com/codiebitcoin/codie-video-player")
						}
					})
				})
			})
		})
	})
}

// settingsSection is one block of settings: its name over its rows.
func (a *app) settingsSection(c *ui.Context, name string, rows func()) {
	ui.Column(c).Shrink(0).Padding(0, 0, 18, 0).Gap(2).Children(func() {
		ui.Text(c, i18n.T(name)).FontSize(13).FontWeight(700).TextColor(colors.muted).Padding(0, 0, 8, 0)
		rows()
	})
}

// settingsRow is one setting: what it is on the left, its control on the
// right, with a word of why under the name.
func (a *app) settingsRow(c *ui.Context, name, note string, control func()) {
	ui.Row(c).MinHeight(40).Padding(10, 12).Gap(12).AlignItems(ui.Center).
		Background(colors.hover).Radius(10).Margin(0, 0, 4, 0).Children(func() {
		ui.Column(c).Grow(1).Shrink(1).MinWidth(0).Gap(2).Children(func() {
			ui.Text(c, name).FontSize(13.5).FontWeight(600)
			if note != "" {
				ui.Text(c, note).FontSize(12).TextColor(colors.muted)
			}
		})
		if control != nil {
			control()
		}
	})
}

// pluginDisabled reports whether a plugin is turned off.
func (a *app) pluginDisabled(id string) bool {
	for _, d := range a.settings.DisabledPlugins {
		if d == id {
			return true
		}
	}
	return false
}

// setPluginDisabled turns a plugin off or on, and the sources with it.
func (a *app) setPluginDisabled(id string, disabled bool) {
	on := a.pluginDisabled(id)
	if disabled == on {
		return
	}
	if disabled {
		a.settings.DisabledPlugins = append(a.settings.DisabledPlugins, id)
	} else {
		for i, d := range a.settings.DisabledPlugins {
			if d == id {
				a.settings.DisabledPlugins = append(a.settings.DisabledPlugins[:i], a.settings.DisabledPlugins[i+1:]...)
				break
			}
		}
	}
	a.settings.save()
	a.makeSources(a.bundled)
	a.loadTabs()
	a.loadTab()
}

// installPluginDialog asks for a plugin's folder, and takes it into the
// plugins' folder when it looks like one.
func (a *app) installPluginDialog() {
	paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
		Title:     i18n.T("Choose a plugin folder (with a plugin.json)"),
		Directory: true,
	})
	if err != nil || len(paths) == 0 {
		return
	}
	src := paths[0]
	name := filepath.Base(src)
	if _, err := os.Stat(filepath.Join(src, "plugin.json")); err != nil {
		log.Printf("install plugin: %s has no plugin.json", name)
		return
	}
	dst := filepath.Join(pluginsDir(), name)
	if err := os.CopyFS(dst, os.DirFS(src)); err != nil {
		log.Printf("install plugin: %v", err)
		return
	}
	a.makeSources(a.bundled)
	a.loadTabs()
	a.loadTab()
	if a.settingsWin != nil {
		a.settingsWin.Invalidate()
	}
}

// updateLine says what the last check for updates made of it.
func updateLine(tag string, checking bool) string {
	if checking {
		return i18n.T("Asking the repo for a newer release…")
	}
	if tag != "" {
		return i18n.Tf("New %s is out — the red dot in the corner takes you there", tag)
	}
	return i18n.T("Up to date")
}
