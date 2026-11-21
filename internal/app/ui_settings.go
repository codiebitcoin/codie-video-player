package app

import (
	"log"
	"os"
	"path/filepath"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
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
		Title:           "设置 — Codie 视频",
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
	t := *c.Theme()
	t.Dark = true
	c.SetTheme(&t)
	c.Root().Background(colors.bg)

	ui.Column(c).Fill().Children(func() {
		ui.Row(c).Height(topBarHeight).Padding(12, 16, 4, 16).Gap(10).AlignItems(ui.Center).DragWindow().Children(func() {
			if b := iconButton(c.Key("settings-close"), iconX, 14, colors.muted, "关闭设置"); b.Clicked() {
				a.settingsWin.Close()
			}
			ui.Text(c, "设置").FontSize(15).FontWeight(700)
			ui.Spacer(c)
			if a.updateTag != "" {
				b := ui.ButtonBase(c.Key("update")).Height(28).PaddingX(12).Radius(999).Center().
					FocusRing(false).Label("新版本").Background(colors.accent.Alpha(0.18)).Border(1, colors.accent.Alpha(0.5))
				b.Children(func() {
					ui.Text(c, "新版本 "+a.updateTag).FontSize(12.5).FontWeight(600).TextColor(colors.accent)
				})
				if b.Clicked() {
					go mygo.Shell.OpenExternal(a.updateURL)
				}
			}
		})
		ui.Scroll(c.Key("settings-scroll")).Fill().Padding(10, 24, 32, 24).Children(func() {
			ui.Column(c).Fill().Gap(4).Children(func() {
				a.settingsSection(c, "播放", func() {
					a.settingsRow(c, "连播", "一集放完,下一集自己接上", func() {
						on := !a.settings.NoAutoNext
						if ui.Switch(c.Key("auto-next"), &on).Changed() {
							a.settings.NoAutoNext = !on
							a.settings.save()
						}
					})
					a.settingsRow(c, "默认倍速", "每一次打开都按这个快慢放", func() {
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
					a.settingsRow(c, "音量", "记着上次的音量,从这再来", func() {
						ui.Slider(c.Key("vol"), &a.vol, 0, 100).Width(160)
					})
				})
				a.settingsSection(c, "插件", func() {
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
					a.settingsRow(c, "安装插件", "一个 plugin.json 和 index.js 的文件夹", func() {
						if ui.Button(c.Key("install"), "选择文件夹…").Clicked() {
							a.installPluginDialog()
						}
					})
					a.settingsRow(c, "插件目录", "把插件的文件夹放进去也行", func() {
						if ui.Button(c.Key("open-dir"), "打开").Clicked() {
							go mygo.Shell.OpenPath(pluginsDir())
						}
					})
				})
				a.settingsSection(c, "关于", func() {
					a.settingsRow(c, "版本", mygo.App.Version(), nil)
					a.settingsRow(c, "检查更新", updateLine(a.updateTag, a.updateChecking), func() {
						label := "检查"
						if a.updateChecking {
							label = "…"
						}
						if ui.Button(c.Key("check"), label).Clicked() {
							a.checkUpdatesNow()
						}
					})
					a.settingsRow(c, "仓库", "github.com/codiebitcoin/codie-video-player", func() {
						if ui.Button(c.Key("repo"), "打开").Clicked() {
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
		ui.Text(c, name).FontSize(13).FontWeight(700).TextColor(colors.muted).Padding(0, 0, 8, 0)
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
		Title:     "选择插件文件夹(含 plugin.json)",
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
		return "正在看仓库上有没有新的…"
	}
	if tag != "" {
		return "有新的 " + tag + " — 设置窗右上角可去取"
	}
	return "已是最新"
}
