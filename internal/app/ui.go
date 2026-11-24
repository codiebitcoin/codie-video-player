package app

import (
	"fmt"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"codie-video-player/internal/i18n"
	"codie-video-player/internal/library"
	"codie-video-player/internal/source/local"
	"codie-video-player/internal/video"
)

// The window: a dark page whose picture takes the whole stage, its controls
// floating over it and stepping out of the way, a drama's episodes at hand
// in a drawer. Everything is the app's own drawing; the window's buttons sit
// over the top bar.

// topBarHeight is the least height of the strip along the top.
const topBarHeight = 40

// view is the content of the window.
func (a *app) view(c *ui.Context) {
	a.applyTheme(c)
	root := c.Root()

	// The keys the whole window answers, wherever the focus sits.
	root.OnShortcut(0, ui.KeySpace, func() {
		if a.page == pagePlay && !a.pipActive() {
			a.playPause()
		}
	})
	root.OnShortcut(0, ui.KeyEscape, func() {
		switch {
		case a.fullscreen:
			a.setFullscreen(false)
		case a.drawerOpen || a.speedOpen:
			a.drawerOpen, a.speedOpen = false, false
			a.wakeControls()
		case a.askSignIn:
			a.askSignIn = false
		case a.page != pageList:
			a.back()
		}
	})
	root.OnShortcut(0, ui.KeyLeft, func() { a.shortcutSeek(-5 * time.Second) })
	root.OnShortcut(0, ui.KeyRight, func() { a.shortcutSeek(5 * time.Second) })
	root.OnShortcut(0, ui.KeyUp, func() { a.shortcutVolume(5) })
	root.OnShortcut(0, ui.KeyDown, func() { a.shortcutVolume(-5) })
	root.OnShortcut(0, ui.KeyF, func() {
		if a.page == pagePlay {
			a.toggleFullscreen()
		}
	})
	root.OnShortcut(ui.Cmd, ui.KeyF, func() {
		if a.page != pagePlay {
			a.searchBox.Focus(c)
		}
	})

	// The stage takes the whole window; the pages share it with the sidebar
	// of sources and pages, as the family's apps have it.
	ui.Row(c).Fill().AlignItems(ui.Stretch).Children(func() {
		if a.page != pagePlay {
			a.sidebar(c)
		}
		ui.Column(c.Key("main")).Grow(1).MinWidth(0).Children(func() {
			// The play page has no top bar: the stage takes the whole window,
			// and a strip of its own fades in at the top with the controls.
			if a.page != pagePlay {
				a.topBar(c)
			}
			id := fmt.Sprintf("page:%d:%s", a.page, a.detailOf)
			page := ui.Box(c.Key(id)).Grow(1).MinHeight(0)
			page.Children(func() {
				ui.Column(c).Fill().Children(func() {
					switch a.page {
					case pagePlay:
						a.playView(c)
					case pageDetail:
						a.detailView(c)
					default:
						a.listView(c)
					}
				})
			})
		})
	})
	// The dialog that asks for a source's sign-in, over the pages.
	a.signInDialog(c)
	// The dialog for macOS's no to a plugin's programs, whose retry asks
	// the system again.
	a.macDialog(c)
}

// macDialog tells of macOS standing between a plugin and the program it
// started — yt-dlp, ffmpeg — which the system's own ask can let through.
// The retry runs the ask again, and macOS asks to allow it, that once.
func (a *app) macDialog(c *ui.Context) {
	if a.macRetry == nil {
		return
	}
	veil := ui.ButtonBase(c.Key("mac-veil")).Absolute().Top(0).Left(0).Right(0).Bottom(0).
		FocusRing(false).Label(i18n.T("Dismiss"))
	veil.Background(colors.veil)
	if veil.Clicked() {
		a.macRetry = nil
	}
	veil.Children(func() {
		card := ui.Column(c).Width(380).Gap(12).Padding(24).Radius(16).
			Background(colors.panel).Border(1, colors.panelBorder).
			Shadow(0, 24, 60, -12, ui.RGBA(0, 0, 0, 0.6))
		card.Center()
		card.Children(func() {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Icon(c, iconSettings).Size(17, 17).TextColor(colors.accent)
				ui.Text(c, i18n.T("macOS hasn't allowed ffmpeg / yt-dlp")).FontSize(17).FontWeight(800)
			})
			ui.Text(c, i18n.T("Retry asks the system to allow them; allowing once is enough.")).
				FontSize(13).TextColor(colors.muted).TextAlign(ui.Center)
			ui.Row(c).Gap(10).AlignItems(ui.Center).Padding(4, 0, 0, 0).Children(func() {
				if ui.Button(c.Key("later"), i18n.T("Not Now")).Clicked() {
					a.macRetry = nil
				}
				go_ := ui.ButtonBase(c.Key("go")).Height(34).PaddingX(18).Radius(999).Center().
					FocusRing(false).Label(i18n.T("Retry")).Background(colors.accent).TextColor(colors.onAccent)
				go_.Children(func() {
					ui.Text(c, i18n.T("Retry")).FontSize(13.5).FontWeight(700)
				})
				if go_.Clicked() {
					fn := a.macRetry
					a.macRetry = nil
					fn()
				}
			})
		})
	})
}

// shortcutSeek moves the episode on by d, when one is on the stage — here
// or on the device alike.
func (a *app) shortcutSeek(d time.Duration) {
	if a.page == pagePlay && !a.pipActive() {
		a.seekBy(d)
	}
}

// shortcutVolume raises or lowers how loud, when the stage plays.
func (a *app) shortcutVolume(d float64) {
	if a.page == pagePlay && !a.pipActive() {
		a.bumpVolume(d)
	}
}

// topBar is the strip the window buttons sit over: back, forward, the name,
// and the settings and device buttons. It drags the window where nothing
// takes the press. The source picker and the search live in the sidebar.
func (a *app) topBar(c *ui.Context) {
	// The traffic lights sit over the sidebar's top on macOS. Elsewhere the
	// window buttons sit over this strip: Windows keeps minimize, maximize and
	// close at its right end, Linux at its left, so both ends clear them.
	lead, trail := float32(0), float32(0)
	if runtime.GOOS != "darwin" {
		lead, trail = 72, 138
	}
	ui.Row(c).Height(topBarHeight).Padding(12, 16+trail, 4, 16+lead).Gap(12).AlignItems(ui.Center).DragWindow().Children(func() {
		if a.page != pageList {
			if b := iconButton(c.Key("back"), iconBack, 16, colors.text, i18n.T("Back")); b.Clicked() {
				a.back()
			}
		}
		if len(a.fwd) > 0 && a.page == pageList {
			if b := iconButton(c.Key("forward"), iconForward, 16, colors.text, i18n.T("Forward")); b.Clicked() {
				a.forward()
			}
		}
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, iconFilm).Size(17, 17).TextColor(colors.accent)
			ui.Text(c, i18n.T("Codie Video")).FontSize(15).FontWeight(700).SingleLine()
		})
		ui.Spacer(c)
		// Settings: the gear carries a red dot while the repo has a newer release.
		gear := ui.ButtonBase(c.Key("settings")).Size(31, 31).Center().Radius(999).
			Label(i18n.T("Settings")).TextColor(colors.muted)
		bg := ui.Transparent
		if gear.Hovered() {
			bg = ui.RGBA(255, 255, 255, 0.1)
		}
		gear.Background(gear.AnimateColor("bg", bg, ui.Animation{Duration: 120 * time.Millisecond}))
		gear.Children(func() {
			ui.Icon(c, iconSettings).Size(15, 15)
			if a.updateTag != "" {
				ui.Box(c).Size(7, 7).Radius(4).Background(colors.accent).
					Absolute().Top(5).Right(5).IgnoreInput(true)
			}
		})
		if gear.Clicked() {
			a.openSettingsWindow()
		}
		// Connect Device: a dot beside it, green once paired.
		b := ui.ButtonBase(c.Key("device")).Height(30).PaddingX(14).Radius(999).Gap(7).AlignItems(ui.Center).
			FocusRing(false).Label(i18n.T("Connect Device")).TextColor(colors.text)
		if a.codie != nil {
			b.Background(colors.hover).Border(1, ui.Oklch(0.75, 0.17, 145).Alpha(0.5))
		} else {
			b.Background(colors.accent.Alpha(0.16)).Border(1, colors.accent.Alpha(0.5))
		}
		b.Children(func() {
			dot := ui.Oklch(0.75, 0.17, 145)
			if a.codie == nil {
				dot = colors.accent
			}
			ui.Box(c).Size(7, 7).Radius(4).Background(dot)
			ui.Text(c, i18n.T("Connect Device")).FontSize(13).FontWeight(600)
		})
		if b.Clicked() {
			a.openDeviceWindow()
		}
	})
}

// sidebarWidth is the width of the strip of sources and pages at the
// window's left, as the family's apps have it.
const sidebarWidth = 232

// sidebar is the strip at the window's left: which source shows, the search,
// the source's pages, and the listener's account there.
func (a *app) sidebar(c *ui.Context) {
	// Under the window buttons where they sit over the sidebar, as the
	// traffic lights do; elsewhere, as close to the top as to the sides.
	top := float32(14)
	if c.TitleBar().Left > 0 {
		top = max(c.TitleBar().Height, topBarHeight) + 4
	}
	ui.Column(c).Width(sidebarWidth).Shrink(0).Padding(top, 14, 14, 14).Gap(6).DragWindow().Children(func() {
		a.sourcePicker(c)
		a.searchField(c)
		ui.Scroll(c.Key("nav")).Grow(1).MinHeight(0).Gap(2).Children(func() {
			a.navTabs(c)
		})
		a.accountRow(c)
	})
}

// sourcePicker is which of the sources of videos shows, by its name.
func (a *app) sourcePicker(c *ui.Context) {
	if len(a.sources) < 1 {
		return
	}
	names := make([]string, len(a.sources))
	at := 0
	for i := range a.sources {
		names[i] = a.sources[i].name
		if a.sourceEntry == &a.sources[i] {
			at = i
		}
	}
	chosen := names[max(at, 0)]
	if ui.Select(c.Key("source"), &chosen, names).Width(sidebarWidth - 28).Changed() {
		for i, n := range names {
			if n == chosen {
				a.useSource(a.sources[i].id)
			}
		}
	}
}

// searchField is the search field atop the sidebar, which searches every
// source where Enter is pressed, clears by its cross, and, holding nothing,
// lists the last queries in a dropdown, as NSSearchField's menu does.
func (a *app) searchField(c *ui.Context) {
	field := ui.Row(c.Key("search-field")).Height(30).Radius(8).AlignItems(ui.Stretch)
	if c.Focused(a.searchBox) {
		field.Background(colors.bg).Shadow(0, 0, 0, 3, colors.accent.Alpha(0.25)).Border(1, colors.accent.Alpha(0.6))
	} else {
		field.Background(colors.hover).Border(1, colors.hairline)
	}
	field.Children(func() {
		clearable := a.query != ""
		end := float32(9)
		if clearable {
			end = 28
		}
		cb := ui.ComboboxBase(c.Key("query"), &a.query)
		in := cb.Input.Bind(&a.searchBox).Grow(1).MinWidth(0).
			PaddingStart(29).PaddingEnd(end).Placeholder(i18n.T("Search dramas, videos…")).FontSize(13).Label(i18n.T("Search"))
		// The last queries, while the field holds nothing and the focus sits
		// in it; something in it, the dropdown is gone, there being no
		// suggestions to show in its place.
		q := strings.TrimSpace(a.query)
		if in.Focused() && q == "" && len(a.settings.SearchHistory) > 0 {
			cb.SetOpen(true)
		} else {
			cb.SetOpen(false)
		}
		cb.Popup(func(panel ui.Element) {
			panel.AttachTo(field, ui.AnchorBottomStart, ui.AnchorTopStart).
				Margin(6, 0, 0, 0).Padding(6).Gap(2).Width(260).
				Radius(10).Background(colors.panel).Border(1, colors.panelBorder).
				Shadow(0, 10, 30, -8, ui.RGBA(0, 0, 0, 0.5))
			panel.Label(i18n.T("Recent Searches"))
			for _, s := range a.settings.SearchHistory {
				row := cb.Item(s).Height(28).PaddingX(10).Radius(6).AlignItems(ui.Center).
					Justify(ui.Start).Gap(8).Label(i18n.Tf("Search %s", s))
				if row.Highlighted() {
					row.Background(colors.press)
				}
				row.Children(func() {
					ui.Icon(c, iconClock).Size(12, 12).TextColor(colors.faint)
					ui.Text(c, s).FontSize(12.5).TextColor(colors.muted).SingleLine()
				})
			}
		})
		if key, ok := cb.Chosen(); ok {
			cb.SetOpen(false)
			a.searchFromSidebar(key)
		} else if in.Submitted() {
			cb.SetOpen(false)
			a.searchFromSidebar(a.query)
		}
		ui.Icon(c, iconSearch).Size(13, 13).TextColor(colors.muted).Absolute().Left(9).Top(7.5).IgnoreInput(true)
		if clearable {
			x := ui.ButtonBase(c.Key("clear")).Size(22, 22).Center().Absolute().Right(4).Top(4).
				KeepFocus().FocusRing(false).Label(i18n.T("Clear Search"))
			if x.Clicked() {
				cb.SetOpen(false)
				a.query = ""
				if a.searching {
					a.searching = false
					a.searchGroups = nil
					a.clearGrid()
					a.loadTab()
				}
			}
			x.Children(func() {
				ui.Icon(c, iconX).Size(10, 10).TextColor(colors.muted)
			})
		}
	})
}

// searchFromSidebar starts a search from the sidebar's field, which shows on
// the detail page too: back to the list first, where answers show.
func (a *app) searchFromSidebar(q string) {
	if a.page != pageList {
		a.back()
	}
	a.searchAll(q)
}

// navTabs is the sidebar's pages: the source's chips, each a row, and under
// 我的收藏 the listener's own folders and collections, when the source has
// them to show.
func (a *app) navTabs(c *ui.Context) {
	var favs []video.Tab
	for _, t := range a.tabs {
		if strings.HasPrefix(t.ID, "fav:") || strings.HasPrefix(t.ID, "season:") {
			favs = append(favs, t)
			continue
		}
		a.navItem(c.Key(t.ID), navIconOf(t.ID), a.tabLabel(t), a.page == pageList && a.tabID == t.ID && !a.searching, func() { a.useTab(t.ID) })
	}
	if len(favs) > 0 {
		a.favGroup(c, favs)
	}
}

// tabLabel is a chip's words. The local source's chips are named in English,
// which is a key; a plugin's chips come already translated.
func (a *app) tabLabel(t video.Tab) string {
	if a.sourceEntry != nil && a.sourceEntry.id == local.ID {
		return i18n.T(t.Label)
	}
	return t.Label
}

// navIconOf picks the icon of a chip, by what it lists.
func navIconOf(id string) *ui.SVG {
	switch {
	case id == "hot":
		return iconFire
	case id == "rank":
		return iconChart
	case id == "watchlater":
		return iconClock
	case id == "add", strings.HasPrefix(id, "season:"), strings.HasPrefix(id, "dir:"):
		return iconFolder
	default:
		return iconFilm
	}
}

// navItem is one of the sidebar's rows, as the family's apps draw them.
func (a *app) navItem(c *ui.Context, icon *ui.SVG, label string, chosen bool, fn func()) {
	b := ui.ButtonBase(c).Height(32).PaddingX(10).Gap(10).Radius(8).AlignItems(ui.Center).
		Justify(ui.Start).FocusRing(false).Label(label)
	bg := ui.Transparent
	switch {
	case chosen:
		bg = colors.press
	case b.Hovered():
		bg = colors.hover
	}
	b.Background(b.AnimateColor("bg", bg, ui.Animation{Duration: 120 * time.Millisecond}))
	color := colors.text
	if chosen {
		color = colors.accent
	}
	b.Children(func() {
		ui.Icon(c, icon).Size(16, 16).TextColor(color)
		ui.Text(c, label).FontSize(13).FontWeight(500).TextColor(color).SingleLine()
	})
	if b.Clicked() {
		fn()
	}
}

// favGroup is 我的收藏 in the sidebar: the source's folders and collections
// the listener's own, taken in with the group or left open.
func (a *app) favGroup(c *ui.Context, favs []video.Tab) {
	ui.Row(c.Key("fav-head")).AlignItems(ui.Center).Margin(10, 4, 2, 6).Children(func() {
		h := ui.ButtonBase(c.Key("fav-toggle")).Grow(1).Height(24).PaddingX(4).Gap(6).Radius(6).
			AlignItems(ui.Center).Justify(ui.Start).FocusRing(false).Label(i18n.T("My Favorites"))
		if h.Clicked() {
			a.favOpen = !a.favOpen
		}
		h.Children(func() {
			ui.Text(c, i18n.T("My Favorites")).FontSize(11.5).FontWeight(600).TextColor(colors.faint).Grow(1)
			arrow := iconDown
			if !a.favOpen {
				arrow = iconForward
			}
			ui.Icon(c, arrow).Size(11, 11).TextColor(colors.faint)
		})
	})
	if !a.favOpen {
		return
	}
	for _, t := range favs {
		id := t.ID
		a.navItem(c.Key(id), navIconOf(id), t.Label, a.page == pageList && a.tabID == id && !a.searching, func() { a.useTab(id) })
	}
}

// accountRow is the sidebar's foot: the listener's account at this source,
// or the button that signs in, when the source has one.
func (a *app) accountRow(c *ui.Context) {
	e := a.sourceEntry
	if e == nil || e.plugin == nil {
		return
	}
	if a.srcAcct != nil {
		ui.Row(c.Key("account")).Height(34).Padding(6, 10).Gap(9).Radius(8).AlignItems(ui.Center).
			Justify(ui.Start).Children(func() {
			ui.Box(c).Size(22, 22).Shrink(0).Radius(999).Clip().Children(func() {
				bmp := a.covers.get(a.srcAcct.Image)
				if bmp == nil {
					ui.Box(c).Fill().Background(colors.hover).Children(func() {
						ui.Icon(c, iconHeart).Size(11, 11).TextColor(colors.faint)
					})
					return
				}
				ui.Image(c, bmp).Absolute().Top(0).Left(0).Right(0).Bottom(0).Fit(ui.Cover)
			})
			ui.Column(c).Gap(0).Children(func() {
				ui.Text(c, a.srcAcct.Name).FontSize(12.5).FontWeight(600).SingleLine()
				ui.Text(c, e.name).FontSize(10.5).TextColor(colors.faint).SingleLine()
			})
		})
		return
	}
	if e.plugin.Can("signIn") {
		a.navItem(c.Key("sign-in"), iconHeart, i18n.T("Sign In"), false, a.startSignIn)
	}
}

// signInDialog asks for a source's sign-in over the pages: the listener's
// favorites and collections there come with it. Signing in opens the
// source's own page, in a window of its own; closing that is giving up.
func (a *app) signInDialog(c *ui.Context) {
	if !a.askSignIn || a.page == pagePlay || a.sourceEntry == nil || a.sourceEntry.plugin == nil {
		return
	}
	veil := ui.ButtonBase(c.Key("signin-veil")).Absolute().Top(0).Left(0).Right(0).Bottom(0).
		FocusRing(false).Label(i18n.T("Dismiss"))
	veil.Background(colors.veil)
	if veil.Clicked() {
		a.askSignIn = false
	}
	veil.Children(func() {
		card := ui.Column(c).Width(380).Gap(12).Padding(24).Radius(16).
			Background(colors.panel).Border(1, colors.panelBorder).
			Shadow(0, 24, 60, -12, ui.RGBA(0, 0, 0, 0.6))
		card.Center()
		card.Children(func() {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Icon(c, iconHeart).Size(17, 17).TextColor(colors.accent)
				ui.Text(c, i18n.Tf("Sign in to %s", a.sourceEntry.name)).FontSize(17).FontWeight(800)
			})
			ui.Text(c, i18n.T("Your favorites, collections and watch later come with a sign-in.")).
				FontSize(13).TextColor(colors.muted).TextAlign(ui.Center)
			if a.signInNote != "" {
				ui.Text(c, a.signInNote).FontSize(12.5).TextColor(colors.accent).TextAlign(ui.Center)
			}
			ui.Row(c).Gap(10).AlignItems(ui.Center).Padding(4, 0, 0, 0).Children(func() {
				if a.signInBusy {
					ui.Spinner(c).Size(15, 15)
					ui.Text(c, i18n.T("Signing in…")).FontSize(13).TextColor(colors.muted)
					return
				}
				if ui.Button(c.Key("later"), i18n.T("Not Now")).Clicked() {
					a.askSignIn = false
				}
				go_ := ui.ButtonBase(c.Key("go")).Height(34).PaddingX(18).Radius(999).Center().
					FocusRing(false).Label(i18n.T("Sign In")).Background(colors.accent).TextColor(colors.onAccent)
				go_.Children(func() {
					ui.Text(c, i18n.T("Sign In")).FontSize(13.5).FontWeight(700)
				})
				if go_.Clicked() {
					a.startSignIn()
				}
			})
		})
	})
}

// listView is the grid of what the tab lists — the tab picked in the
// sidebar — the viewer's own shelves above them (or a playlist's header when
// the tab is one), and a search's answers in place of it all.
func (a *app) listView(c *ui.Context) {
	ui.Column(c).Fill().Children(func() {
		ui.Scroll(c.Key("grid")).Fill().Padding(10, 20, 32, 20).Children(func() {
			if a.searching {
				a.searchView(c)
			} else if isPlaylistTab(a.tabID) {
				a.playlistHeader(c)
				a.grid(c)
			} else {
				a.shelves(c)
				a.grid(c)
			}
		})
	})
	// The first run's hello, once, over the list page.
	if !a.settings.WizardDone {
		a.wizard(c)
	}
}

// playlistHeader sits over a playlist tab (favorites folder, saved collection,
// or watch-later): its cover, its name and count, the Play / Shuffle / Play on
// codie buttons, and a search field to filter within the playlist.
func (a *app) playlistHeader(c *ui.Context) {
	items := a.playlistItems()
	ui.Row(c).Gap(24).AlignItems(ui.End).Padding(4, 0, 22, 0).Children(func() {
		cover := ui.Box(c.Key("pl-cover")).Size(136, 136).Shrink(0).Radius(14).
			Shadow(0, 14, 32, -8, ui.RGBA(0, 0, 0, 0.4)).Clip().Center()
		if len(a.items) > 0 && a.items[0].Cover != "" {
			cover.Children(func() {
				a.coverFill(c, a.items[0].Cover)
			})
		} else {
			cover.Background(colors.accent.Alpha(0.18)).Border(1, colors.accent.Alpha(0.4))
			cover.Children(func() {
				icon := iconHeartFill
				if strings.HasPrefix(a.tabID, "season:") {
					icon = iconFolder
				} else if a.tabID == "watchlater" {
					icon = iconClock
				}
				ui.Icon(c, icon).Size(44, 44).TextColor(colors.accent)
			})
		}
		ui.Column(c).Grow(1).Shrink(1).MinWidth(0).Gap(6).Children(func() {
			ui.Text(c, i18n.T("Playlist")).FontSize(12).FontWeight(600).TextColor(colors.accent)
			ui.Text(c, a.currentTabLabel()).FontSize(26).FontWeight(800).LetterSpacing(-0.5).MaxLines(2).LineHeight(1.2)
			if !a.gridLoading || len(a.items) > 0 {
				ui.Text(c, i18n.Tf("%d videos", len(items))).FontSize(13).TextColor(colors.muted)
			}
			ui.Row(c).Gap(10).AlignItems(ui.Center).Margin(8, 0, 0, 0).Children(func() {
				// Play: plays the playlist in order from the first shown video.
				play := ui.ButtonBase(c.Key("pl-play")).Height(34).PaddingX(16).Radius(999).Center().Gap(7).
					FocusRing(false).Label(i18n.T("Play")).Background(colors.accent).TextColor(colors.onAccent)
				play.Children(func() {
					ui.Icon(c, iconPlay).Size(13, 13)
					ui.Text(c, i18n.T("Play")).FontSize(13).FontWeight(700)
				})
				if play.Clicked() && len(items) > 0 {
					a.playPlaylist(items, 0, false)
				}
				// Shuffle: plays the playlist in random order.
				shuf := ui.ButtonBase(c.Key("pl-shuffle")).Height(34).PaddingX(15).Radius(999).Center().Gap(7).
					FocusRing(false).Label(i18n.T("Shuffle")).Background(colors.hover).
					Border(1, colors.hairline).TextColor(colors.text)
				shuf.Children(func() {
					ui.Icon(c, iconShuffle).Size(13, 13)
					ui.Text(c, i18n.T("Shuffle")).FontSize(13).FontWeight(600)
				})
				if shuf.Clicked() && len(items) > 0 {
					a.playPlaylist(items, 0, true)
				}
				// Play on codie: plays the playlist on the device using the chosen character slot.
				if a.codie != nil {
					codie := ui.ButtonBase(c.Key("pl-codie")).Height(34).PaddingX(15).Radius(999).Center().Gap(7).
						FocusRing(false).Label(i18n.T("Play on codie")).Background(colors.hover).
						Border(1, colors.hairline).TextColor(colors.text)
					codie.Children(func() {
						ui.Icon(c, iconPip).Size(13, 13)
						ui.Text(c, i18n.T("Play on codie")).FontSize(13).FontWeight(600)
					})
					if codie.Clicked() && len(items) > 0 {
						a.playPlaylistOnCodie(items, 0)
					}
				}
				// Search within the playlist.
				if len(a.items) > 0 || a.playlistQuery != "" {
					a.playlistSearchField(c)
				}
			})
		})
	})
}

// playlistSearchField filters the showing playlist's items by title or uploader.
func (a *app) playlistSearchField(c *ui.Context) {
	field := ui.Row(c.Key("pl-search")).Width(210).Height(34).Radius(999).AlignItems(ui.Stretch)
	if c.Focused(a.playlistBox) {
		field.Background(colors.bg).Shadow(0, 0, 0, 3, colors.accent.Alpha(0.25)).Border(1, colors.accent.Alpha(0.6))
	} else {
		field.Background(colors.hover).Border(1, colors.hairline)
	}
	field.Children(func() {
		clearable := a.playlistQuery != ""
		end := float32(12)
		if clearable {
			end = 28
		}
		ui.TextInputBase(c.Key("pl-q"), &a.playlistQuery).Bind(&a.playlistBox).Grow(1).MinWidth(0).
			PaddingStart(30).PaddingEnd(end).Placeholder(i18n.T("Search in playlist…")).
			FontSize(12.5).Label(i18n.T("Search in playlist…"))
		ui.Icon(c, iconSearch).Size(13, 13).TextColor(colors.muted).Absolute().Left(11).Top(9.5).IgnoreInput(true)
		if clearable {
			x := ui.ButtonBase(c.Key("pl-clear")).Size(22, 22).Center().Absolute().Right(6).Top(5).
				KeepFocus().FocusRing(false).Label(i18n.T("Clear Playlist Search"))
			if x.Clicked() {
				a.playlistQuery = ""
			}
			x.Children(func() {
				ui.Icon(c, iconX).Size(10, 10).TextColor(colors.muted)
			})
		}
	})
}

// searchView is what a search found: a chip to a source that had any (plus
// Playlists when the listener has any), over their answers together.
func (a *app) searchView(c *ui.Context) {
	pls := a.matchingPlaylists(a.query)
	allPls := a.allPlaylists()
	plCount := len(pls)
	if plCount == 0 {
		plCount = len(allPls)
	}
	if len(a.searchGroups) > 1 || len(allPls) > 0 {
		ui.Row(c).Shrink(0).Padding(0, 0, 12, 0).Gap(8).AlignItems(ui.Center).Children(func() {
			a.searchChip(c, "all", i18n.T("All"), a.countSearchItems()+len(pls))
			if len(allPls) > 0 {
				a.searchChip(c, "playlists", i18n.T("Playlists"), plCount)
			}
			for _, g := range a.searchGroups {
				if len(g.items) > 0 {
					a.searchChip(c, g.id, g.name, len(g.items))
				}
			}
		})
	}
	if a.searchFilter == "playlists" {
		showPls := pls
		if len(showPls) == 0 {
			showPls = allPls
		}
		if len(showPls) == 0 {
			a.centered(c, i18n.Tf("Nothing found for “%s”", a.query), "", nil)
			return
		}
		a.playlistSearchCards(c, showPls)
		return
	}
	if a.searchFilter == "all" && len(pls) > 0 {
		ui.Column(c).Shrink(0).Padding(0, 0, 18, 0).Gap(10).Children(func() {
			ui.Row(c).Gap(7).AlignItems(ui.Center).Children(func() {
				ui.Icon(c, iconFolder).Size(14, 14).TextColor(colors.accent)
				ui.Text(c, i18n.T("Playlists")).FontSize(15).FontWeight(700)
				ui.Text(c, strconv.Itoa(len(pls))).FontSize(12).TextColor(colors.faint)
			})
			a.playlistSearchCards(c, pls)
		})
	}
	items := a.searchItems()
	probs := a.searchProblems()
	switch {
	case a.gridLoading && len(a.searchGroups) == 0:
		a.loading(c)
	case len(items) == 0 && len(pls) == 0 && probs != "" && a.countSearchHits() == 0:
		a.centered(c, probs, i18n.T("Try Again"), func() { a.searchAll(a.query) })
	case len(items) == 0 && len(pls) == 0 && len(a.searchGroups) > 0:
		a.centered(c, i18n.Tf("Nothing found for “%s”", a.query), "", nil)
	case len(items) == 0 && len(pls) == 0:
		a.loading(c)
	case len(items) > 0:
		ui.Grid(c).Columns(columns(c, 150)).GapX(16).GapY(24).Children(func() {
			for i := range items {
				idx := i
				a.cardWithPlay(c.Key("s"+items[i].ID+":"+strconv.Itoa(i)), &items[i], func() {
					a.playItemFromGrid(items, idx)
				}, func() {
					a.clearQueue()
					a.openDetail(items[idx])
				})
			}
		})
		if a.gridLoading {
			ui.Row(c).Center().Padding(14, 0, 0, 0).Children(func() {
				ui.Spinner(c).Size(16, 16)
			})
		}
		if probs != "" {
			ui.Text(c, probs).FontSize(12).TextColor(colors.faint).Padding(14, 0, 0, 0)
		}
	}
}

// playlistSearchCards draws cards for matching playlists in search results:
// clicking the play button on a card plays the playlist; clicking the card
// opens the playlist tab.
func (a *app) playlistSearchCards(c *ui.Context, pls []video.Tab) {
	ui.Grid(c).Columns(columns(c, 150)).GapX(16).GapY(20).Children(func() {
		for _, pl := range pls {
			tab := pl
			card := ui.ButtonBase(c.Key("plcard-" + tab.ID)).Column().Gap(8).
				AlignItems(ui.Stretch).Justify(ui.Start).FocusRing(false).Label(tab.Label)
			hovered := card.Hovered()
			playClicked := false
			card.Children(func() {
				art := ui.Box(c).AspectRatio(1).Radius(12).Background(colors.hover).Border(1, colors.hairline)
				lift := ui.Pose{}
				if hovered {
					lift = ui.Pose{Y: -3}
					art.Shadow(0, 12, 28, -6, ui.RGBA(0, 0, 0, 0.45))
				}
				art.AnimatePose(lift, ui.Snappy)
				art.Children(func() {
					ui.Row(c).Absolute().Top(0).Left(0).Right(0).Bottom(0).Justify(ui.Center).AlignItems(ui.Center).Children(func() {
						icon := iconHeartFill
						if strings.HasPrefix(tab.ID, "season:") {
							icon = iconFolder
						} else if tab.ID == "watchlater" {
							icon = iconClock
						}
						ui.Icon(c, icon).Size(36, 36).TextColor(colors.accent)
					})
					if a.cardPlayButton(c, hovered) {
						playClicked = true
					}
				})
				ui.Text(c, tab.Label).FontSize(13.5).FontWeight(600).MaxLines(2).LineHeight(1.35)
				ui.Text(c, i18n.T("Playlist")).FontSize(12).TextColor(colors.muted).SingleLine().Margin(-4, 0, 0, 0)
			})
			if playClicked {
				a.openAndPlayTab(tab.ID)
			} else if card.Clicked() {
				a.useTab(tab.ID)
			}
		}
	})
}

// countSearchHits is how many sources came back with something.
func (a *app) countSearchHits() int {
	n := 0
	for _, g := range a.searchGroups {
		if len(g.items) > 0 {
			n++
		}
	}
	return n
}

// searchChip is one source's part of a search's answers, raised when chosen.
func (a *app) searchChip(c *ui.Context, id, name string, n int) {
	chosen := a.searchFilter == id
	b := ui.ButtonBase(c.Key("f" + id)).Height(28).PaddingX(12).Radius(999).Center().Gap(6).
		FocusRing(false).Label(i18n.Tf("Source %s", name))
	switch {
	case chosen:
		b.Background(colors.accent).TextColor(colors.onAccent)
	case b.Hovered():
		b.Background(colors.press)
	default:
		b.Background(colors.hover)
	}
	b.Children(func() {
		ui.Text(c, name).FontSize(12.5).FontWeight(600)
		ui.Text(c, strconv.Itoa(n)).FontSize(11.5).TextColor(colors.faint)
	})
	if b.Clicked() {
		a.searchFilter = id
	}
}

// countSearchItems is how many answers a search found in every source.
func (a *app) countSearchItems() int {
	n := 0
	for _, g := range a.searchGroups {
		n += len(g.items)
	}
	return n
}

// shelves are the viewer's own rows over the grid: what they follow, and
// what they watched lately, each drama a card that goes on where it was.
// Both rows are of the source showing: a drama followed on another source
// stays with that source.
func (a *app) shelves(c *ui.Context) {
	if a.lib == nil || a.sourceEntry == nil {
		return
	}
	follows, history := a.lib.Of(a.sourceEntry.id)
	if len(follows) > 0 {
		a.shelf(c, i18n.T("Following"), iconHeart, follows)
	}
	if len(history) > 0 {
		a.shelf(c, i18n.T("Continue Watching"), iconClock, history)
	}
}

// shelf is one of the viewer's rows: its name over a line of cards that
// scrolls sideways.
func (a *app) shelf(c *ui.Context, name string, icon *ui.SVG, entries []library.Watched) {
	n := len(entries)
	if n > 12 {
		n = 12
	}
	ui.Column(c).Shrink(0).Padding(4, 0, 14, 0).Gap(8).Children(func() {
		ui.Row(c).Gap(7).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, icon).Size(14, 14).TextColor(colors.accent)
			ui.Text(c, name).FontSize(15).FontWeight(700)
			ui.Text(c, strconv.Itoa(len(entries))).FontSize(12).TextColor(colors.faint)
		})
		ui.ScrollHorizontal(c.Key("shelf-" + name)).Children(func() {
			ui.Row(c).Gap(12).Children(func() {
				for i := 0; i < n; i++ {
					e := entries[i]
					a.shelfCard(c.Key("e"+e.ID+":"+strconv.Itoa(i)), e)
				}
			})
		})
	})
}

// shelfCard is one drama of a shelf: its cover, its name, and how far into
// it the viewer is, going on where it stopped when taken.
func (a *app) shelfCard(c *ui.Context, e library.Watched) {
	b := ui.ButtonBase(c).Width(122).Column().Gap(6).AlignItems(ui.Stretch).FocusRing(false).Label(e.Title)
	b.Children(func() {
		art := ui.Box(c).AspectRatio(3.0 / 4.0).Radius(8).Children(func() {
			a.coverFill(c, e.Cover)
		})
		if b.Hovered() {
			art.Shadow(0, 10, 24, -6, ui.RGBA(0, 0, 0, 0.45))
		}
		ui.Text(c, e.Title).FontSize(12.5).FontWeight(500).MaxLines(1).SingleLine()
		ui.Text(c, shelfCaption(e)).FontSize(11).TextColor(colors.muted).SingleLine()
	})
	if b.Clicked() {
		a.resumeWatch(e)
	}
}

// shelfCaption says how far a watched drama is: which episode, and whether
// it came to its end.
func shelfCaption(e library.Watched) string {
	if e.Done {
		return i18n.T("Finished")
	}
	at := i18n.Tf("Ep %d", e.EpAt+1)
	if e.Ep != "" {
		return at + " · " + e.Ep
	}
	return at
}

// resumeWatch takes a drama of the shelves up where it stopped.
func (a *app) resumeWatch(e library.Watched) {
	ep := video.Episode{ID: e.EpID, Title: e.Ep}
	if e.EpID == "" {
		ep = video.Episode{}
	}
	a.playEpisode(e.Item, ep, e.EpAt)
}

// grid is the dramas as cards, so many to a row as fit, with what the page
// says while it loads, or why it didn't.
func (a *app) grid(c *ui.Context) {
	items := a.items
	if isPlaylistTab(a.tabID) {
		items = a.playlistItems()
	}
	switch {
	case a.gridErr != "" && len(a.items) == 0:
		a.gridError(c, a.gridErr)
	case a.tabsErr != "" && len(a.items) == 0:
		a.gridError(c, a.tabsErr)
	case (a.gridLoading || a.tabsLoading) && len(a.items) == 0:
		a.skeleton(c)
	case len(a.items) > 0 && len(items) == 0 && a.playlistQuery != "":
		a.centered(c, i18n.Tf("Nothing found for “%s”", a.playlistQuery), "", nil)
	case len(a.items) == 0 && a.tabID != "":
		note := i18n.T("Nothing here yet")
		if a.sourceEntry != nil && a.sourceEntry.id == local.ID && a.tabID == "add" {
			note = i18n.T("Add a folder of videos to play it here")
		}
		a.centered(c, note, "", nil)
	case len(a.items) == 0:
		a.skeleton(c)
	default:
		a.cards(c)
		if a.gridLoading {
			ui.Row(c).Center().Padding(14, 0, 0, 0).Children(func() {
				ui.Spinner(c).Size(16, 16)
			})
		} else if a.gridMore && a.playlistQuery == "" {
			ui.Row(c).Center().Padding(16, 0, 0, 0).Children(func() {
				if ui.Button(c.Key("more"), i18n.T("Load More")).Clicked() {
					a.loadMore()
				}
			})
		}
	}
}

// gridError says why a page didn't load, with a try again, and a way out to
// the other sources when there are any.
func (a *app) gridError(c *ui.Context, msg string) {
	ui.Column(c).Fill().Center().Gap(12).Children(func() {
		ui.Icon(c, iconFilm).Size(30, 30).TextColor(colors.faint)
		ui.Text(c, msg).TextColor(colors.muted).FontSize(13).TextAlign(ui.Center).MaxWidth(520)
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			if ui.Button(c.Key("retry"), i18n.T("Retry")).Clicked() {
				a.loadTabs()
				a.loadTab()
			}
			for i := range a.sources {
				e := &a.sources[i]
				if a.sourceEntry != nil && e.id == a.sourceEntry.id {
					continue
				}
				if ui.Button(c.Key("go"+e.id), i18n.Tf("Switch to %s", e.name)).Clicked() {
					a.useSource(e.id)
				}
			}
		})
	})
}

// skeleton is the page's shape while its first load is on: empty cards
// where the dramas will be.
func (a *app) skeleton(c *ui.Context) {
	ui.Grid(c).Columns(columns(c, 150)).GapX(16).GapY(24).Children(func() {
		for i := 0; i < 15; i++ {
			ui.Column(c.Key("sk" + strconv.Itoa(i))).Gap(8).AlignItems(ui.Stretch).Children(func() {
				ui.Box(c).AspectRatio(3.0 / 4.0).Radius(10).Background(colors.hover)
				ui.Box(c).Height(13).Width(110).Radius(5).Background(colors.hover)
				ui.Box(c).Height(10).Width(70).Radius(5).Background(colors.hover)
			})
		}
	})
}

// cards is a page of dramas: a poster over its title and a line under that,
// opening the drama's page (or playing from the hover play button).
func (a *app) cards(c *ui.Context) {
	items := a.items
	playlist := isPlaylistTab(a.tabID)
	if playlist {
		items = a.playlistItems()
	}
	cols := columns(c, 150)
	ui.Grid(c).Columns(cols).GapX(16).GapY(24).Children(func() {
		for i := range items {
			idx := i
			// The site gives some lists dramas without an id, so the place in
			// the grid keeps the key apart too.
			a.cardWithPlay(c.Key("d"+items[i].ID+":"+strconv.Itoa(i)), &items[i], func() {
				a.playItemFromGrid(items, idx)
			}, func() {
				if playlist {
					a.setQueue(items, idx, a.currentTabLabel())
				} else {
					a.clearQueue()
				}
				a.openDetail(items[idx])
			})
		}
	})
}

// card is a drama as the lists show it, with how far it's watched on its
// cover when it is.
func (a *app) card(c *ui.Context, it *video.Item) {
	a.cardWithPlay(c, it, nil, func() { a.openDetail(*it) })
}

// cardWithPlay is a drama card with an optional hover play button on the
// cover's corner, as the music player's cards have.
func (a *app) cardWithPlay(c *ui.Context, it *video.Item, play, open func()) {
	card := ui.ButtonBase(c).Column().Gap(8).AlignItems(ui.Stretch).Justify(ui.Start).FocusRing(false).Label(it.Title)
	hovered := card.Hovered()
	playClicked := false
	card.Children(func() {
		art := ui.Box(c).AspectRatio(3.0 / 4.0).Radius(10)
		lift := ui.Pose{}
		if hovered {
			lift = ui.Pose{Y: -3}
			art.Shadow(0, 12, 28, -6, ui.RGBA(0, 0, 0, 0.45))
		} else {
			art.Shadow(0, 2, 8, -2, ui.RGBA(0, 0, 0, 0.3))
		}
		art.AnimatePose(lift, ui.Snappy)
		art.Children(func() {
			a.coverFill(c, it.Cover)
			if it.Cover == "" {
				ui.Row(c).Absolute().Top(0).Left(0).Right(0).Bottom(0).Justify(ui.Center).AlignItems(ui.Center).Children(func() {
					ui.Icon(c, iconFilm).Size(30, 30).TextColor(colors.faint)
				})
			}
			if p := a.lib.ProgressOf(it.ID); p != nil && !p.Done && (p.EpAt > 0 || p.Pos > 0) {
				ui.Box(c).Absolute().Left(6).Bottom(6).Height(18).PaddingX(7).Radius(9).
					Background(ui.RGBA(0, 0, 0, 0.66)).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
					ui.Text(c, cardBadge(p)).FontSize(10.5).TextColor(ui.RGB(255, 255, 255)).SingleLine()
				})
			}
			if play != nil && a.cardPlayButton(c, hovered) {
				playClicked = true
			}
		})
		ui.Text(c, it.Title).FontSize(13).FontWeight(500).MaxLines(2).LineHeight(1.35)
		if it.Subtitle != "" {
			ui.Text(c, it.Subtitle).FontSize(12).TextColor(colors.muted).SingleLine().Margin(-6, 0, 0, 0)
		}
	})
	if playClicked {
		play()
	} else if card.Clicked() && open != nil {
		open()
	}
}

// cardPlayButton is the floating round play button on a card's cover when
// hovered, playing the card or playlist directly without opening its page.
func (a *app) cardPlayButton(c *ui.Context, shown bool) bool {
	b := ui.ButtonBase(c.Key("cplay")).Absolute().Bottom(8).Right(8).Size(34, 34).Radius(999).Center().
		Background(colors.accent).TextColor(colors.onAccent).Label(i18n.T("Play")).FocusRing(false).
		Shadow(0, 4, 12, 0, ui.RGBA(0, 0, 0, 0.35))
	pose := ui.Pose{Fade: 1, Scale: 0.8, Y: 6}
	if shown || b.Hovered() {
		pose = ui.Pose{Scale: 1}
		if b.Hovered() {
			pose.Scale = 1.06
		}
	}
	b.AnimatePose(pose, ui.Snappy)
	b.Children(func() { ui.Icon(c, iconPlay).Size(14, 14).Margin(0, 0, 0, 1.5) })
	return b.Clicked()
}

// cardBadge says on a cover which episode is on, and how far into it.
func cardBadge(p *library.Watched) string {
	s := i18n.Tf("Ep %d", p.EpAt+1)
	if p.Dur > 0 && p.Pos > 0 {
		s += " " + strconv.Itoa(int(p.Pos/p.Dur*100)) + "%"
	}
	return s
}

// coverFill fills its parent with the picture of a cover address, its
// corners rounded by radius, as far faded in as it has come.
func (a *app) coverFill(c *ui.Context, url string) {
	bmp := a.covers.get(url)
	const radius = 10
	box := ui.Box(c).Absolute().Top(0).Left(0).Right(0).Bottom(0).Radius(radius)
	if bmp == nil {
		box.Background(colors.hover)
		box.Children(func() {
			ui.Icon(c, iconFilm).Size(28, 28).TextColor(colors.faint)
		})
		return
	}
	in := box.AnimateTo("art", 1, ui.Animation{Duration: 320 * time.Millisecond})
	ui.Image(c, bmp).Absolute().Top(0).Left(0).Right(0).Bottom(0).Fit(ui.Cover).Radius(radius).Opacity(in)
}

// detailView is a drama's page: its cover over a breath of the same
// picture, what it is, and its episodes one button to each.
func (a *app) detailView(c *ui.Context) {
	d := a.detail
	if d == nil {
		return
	}
	ui.Scroll(c.Key("detail")).Fill().Children(func() {
		ui.Column(c).Fill().Padding(10, 24, 32, 24).Children(func() {
			ui.Row(c).Gap(28).AlignItems(ui.Start).Padding(0, 0, 10, 0).Children(func() {
				ui.Box(c).Width(220).Shrink(0).AspectRatio(3.0/4.0).Radius(12).Shadow(0, 8, 24, -6, ui.RGBA(0, 0, 0, 0.45)).Children(func() {
					a.coverFill(c, d.Cover)
				})
				ui.Column(c).Grow(1).Shrink(1).MinWidth(0).Gap(8).Padding(6, 0, 0, 0).Children(func() {
					ui.Text(c, d.Title).FontSize(26).FontWeight(800).LetterSpacing(-0.4).MaxLines(2).LineHeight(1.2)
					meta := d.Subtitle
					if d.Finished {
						meta = strings.TrimSpace(meta + " · " + i18n.T("Completed"))
					} else if len(d.Episodes) > 0 {
						meta = strings.TrimSpace(meta + " · " + i18n.T("Ongoing"))
					}
					if meta != "" {
						ui.Text(c, meta).FontSize(13).TextColor(colors.muted)
					}
					ui.Row(c).Gap(10).AlignItems(ui.Center).Padding(4, 0, 0, 0).Children(func() {
						if d.Score != "" {
							ui.Text(c, i18n.Tf("Score %s", d.Score)).FontSize(13).TextColor(colors.accent).FontWeight(600)
						}
						heart := iconHeart
						hcolor := colors.muted
						if a.lib.Followed(d.ID) {
							heart = iconHeartFill
							hcolor = colors.accent
						}
						fb := ui.ButtonBase(c.Key("follow")).Height(32).PaddingX(14).Radius(999).Center().Gap(7).
							FocusRing(false).Label(i18n.T("Follow")).Border(1, colors.hairline).TextColor(hcolor)
						fb.Background(colors.hover)
						fb.Children(func() {
							ui.Icon(c, heart).Size(14, 14)
							ui.Text(c, map[bool]string{true: i18n.T("Following"), false: i18n.T("Follow")}[a.lib.Followed(d.ID)]).FontSize(13).FontWeight(600)
						})
						if fb.Clicked() {
							a.lib.ToggleFollow(d.Item)
							a.win.Invalidate()
						}
					})
					if d.Intro != "" {
						ui.Text(c, d.Intro).FontSize(13).TextColor(colors.muted).MaxLines(6).LineHeight(1.5).Margin(4, 0, 0, 0)
					}
					a.detailActions(c, d)
				})
			})
			switch {
			case a.detailLoading && len(d.Episodes) == 0:
				a.loading(c)
			case a.detailErr != "" && len(d.Episodes) == 0:
				a.centered(c, a.detailErr, i18n.T("Retry"), func() { a.openDetail(d.Item) })
			case len(d.Episodes) == 0:
				a.centered(c, i18n.T("No playable episodes for this drama"), "", nil)
			default:
				a.episodes(c, d)
			}
		})
	})
}

// detailActions is the row under what a drama is: the play that goes on
// where it stopped, or the first episode, plus the codie play button and
// inline 5-slot selector when a device is paired.
func (a *app) detailActions(c *ui.Context, d *video.Video) {
	if len(d.Episodes) == 0 {
		return
	}
	p := a.lib.ProgressOf(d.ID)
	ui.Column(c).Gap(10).Padding(10, 0, 0, 0).Children(func() {
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			at, ep := 0, d.Episodes[0]
			label := i18n.T("Play")
			if p != nil && !p.Done && p.Pos > 10 {
				for i := range d.Episodes {
					if d.Episodes[i].ID == p.EpID {
						at, ep = i, d.Episodes[i]
						break
					}
				}
				label = i18n.Tf("Continue · Ep %d", at+1)
				if p.Dur > 0 {
					label += " · " + clock(time.Duration(p.Pos)*time.Second)
				}
			} else if len(d.Episodes) > 1 {
				label = i18n.T("Play Episode 1")
			}
			play := ui.ButtonBase(c.Key("play-first")).Height(36).PaddingX(18).Radius(999).Center().Gap(8).
				FocusRing(false).Label(label).Background(colors.accent).TextColor(colors.onAccent)
			play.Children(func() {
				ui.Icon(c, iconPlay).Size(14, 14)
				ui.Text(c, label).FontSize(13.5).FontWeight(700)
			})
			if play.Clicked() {
				a.playEpisode(d.Item, ep, at)
			}
			// 在 codie 上播放:直接按详情页选好的槽位在设备上开播,不再弹确认框。
			if a.codie != nil {
				codie := ui.ButtonBase(c.Key("play-codie")).Height(36).PaddingX(16).Radius(999).Center().Gap(7).
					FocusRing(false).Label(i18n.T("Play on codie")).Background(colors.hover).
					Border(1, colors.hairline).TextColor(colors.text)
				codie.Children(func() {
					ui.Icon(c, iconPip).Size(14, 14)
					ui.Text(c, i18n.T("Play on codie")).FontSize(13).FontWeight(600).TextColor(colors.text)
				})
				if codie.Clicked() {
					a.startPlayOnCodie(d.Item, ep, at)
				}
			}
		})
		if a.codie != nil {
			a.slotSelector(c)
		}
	})
}

// slotSelector is the inline 5-slot picker on the detail page, choosing which
// character slot the paired codie device stands in.
func (a *app) slotSelector(c *ui.Context) {
	active := a.slotChoice
	if active <= 0 {
		if a.deviceSlot > 0 {
			active = a.deviceSlot
		} else {
			active = 1
		}
	}
	ui.Column(c).Gap(6).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, i18n.T("Character slot")).FontSize(12.5).TextColor(colors.muted)
			for n := 1; n <= codieSlotCount; n++ {
				slot := n
				chosen := active == slot
				b := ui.ButtonBase(c.Key("slot" + strconv.Itoa(slot))).Height(28).PaddingX(12).Radius(999).
					Center().Gap(5).FocusRing(false).Label(i18n.Tf("Slot %d", slot))
				if chosen {
					b.Background(colors.accent).TextColor(colors.onAccent)
				} else {
					b.Background(colors.hover).Border(1, colors.hairline).TextColor(colors.text)
				}
				b.Children(func() {
					ui.Text(c, i18n.Tf("Slot %d", slot)).FontSize(12).FontWeight(600)
				})
				if b.Clicked() && !a.slotBusy {
					a.selectSlot(slot)
				}
			}
			if a.slotBusy {
				ui.Spinner(c).Size(13, 13)
				ui.Text(c, i18n.T("Switching slot…")).FontSize(12).TextColor(colors.muted)
			}
		})
		if a.slotErr != "" {
			ui.Text(c, a.slotErr).FontSize(12).TextColor(colors.accent)
		}
	})
}

// episodes is a drama's episodes: a grid of them, or a list when there are
// too many for one page of buttons.
func (a *app) episodes(c *ui.Context, d *video.Video) {
	p := a.lib.ProgressOf(d.ID)
	ui.Row(c).Margin(14, 0, 10, 0).Children(func() {
		ui.Text(c, i18n.T("Episodes")).FontSize(17).FontWeight(700)
		ui.Spacer(c)
		ui.Text(c, i18n.Tf("%d episodes", len(d.Episodes))).FontSize(13).TextColor(colors.muted)
	})
	if len(d.Episodes) > 60 {
		ui.List(c.Key("eps"), &a.epsList, len(d.Episodes), func(i int) {
			ui.Box(c).Padding(0, 0, 2, 0).Children(func() {
				a.episodeRow(c, d, &d.Episodes[i], i, p)
			})
		}).Grow(1)
		return
	}
	ui.Grid(c).Columns(columns(c, 120)).GapX(10).GapY(10).Children(func() {
		for i := range d.Episodes {
			a.episode(c.Key(d.Episodes[i].ID+":"+strconv.Itoa(i)), d, &d.Episodes[i], i)
		}
	})
}

// episodeRow is one episode as a line of the long list, the one playing
// marked, the one watched told.
func (a *app) episodeRow(c *ui.Context, d *video.Video, ep *video.Episode, at int, p *library.Watched) {
	nowPlaying := a.page == pagePlay && a.playing.item.ID == d.ID && a.playing.ep.ID == ep.ID
	seen := p != nil && (p.Done && p.EpID == ep.ID || p.EpAt > at)
	label := strconv.Itoa(at+1) + "  " + ep.Title
	b := ui.ButtonBase(c.Key("r" + ep.ID + ":" + strconv.Itoa(at))).Height(38).PaddingX(12).Radius(8).
		AlignItems(ui.Center).Justify(ui.Start).Gap(8).FocusRing(false).Label(label)
	switch {
	case nowPlaying:
		b.Background(colors.accent.Alpha(0.25)).Border(1, colors.accent.Alpha(0.6))
	case b.Hovered():
		b.Background(colors.hover)
	default:
		b.Background(colors.press)
	}
	b.Children(func() {
		t := ui.Text(c, label).FontSize(13).Grow(1).Shrink(1).MinWidth(0).SingleLine()
		if nowPlaying {
			t.TextColor(colors.accent).FontWeight(600)
		} else if seen {
			t.TextColor(colors.faint)
		}
		if ep.Seconds > 0 {
			ui.Text(c, clock(time.Duration(ep.Seconds)*time.Second)).FontSize(12).TextColor(colors.faint)
		}
		if nowPlaying {
			ui.Icon(c, iconPlay).Size(12, 12).TextColor(colors.accent)
		}
	})
	if b.Clicked() {
		a.playEpisode(d.Item, *ep, at)
	}
}

// episode is one episode as a button, the one playing marked, the one
// watched told.
func (a *app) episode(c *ui.Context, d *video.Video, ep *video.Episode, at int) {
	nowPlaying := a.page == pagePlay && a.playing.item.ID == d.ID && a.playing.ep.ID == ep.ID
	p := a.lib.ProgressOf(d.ID)
	seen := p != nil && (p.Done && p.EpID == ep.ID || p.EpAt > at)
	label := ep.Title
	b := ui.ButtonBase(c).Height(44).PaddingX(12).Radius(9).AlignItems(ui.Center).Justify(ui.Start).Gap(8).
		FocusRing(false).Label(label)
	switch {
	case nowPlaying:
		b.Background(colors.accent.Alpha(0.25))
		b.Border(1, colors.accent.Alpha(0.6))
	case b.Hovered():
		b.Background(colors.hover)
	default:
		b.Background(colors.press)
	}
	b.Children(func() {
		title := ui.Text(c, label).FontSize(13).Grow(1).Shrink(1).MinWidth(0).SingleLine()
		if nowPlaying {
			title.TextColor(colors.accent).FontWeight(600)
		} else if seen {
			title.TextColor(colors.faint)
		}
		if p != nil && p.EpID == ep.ID && !p.Done && p.Pos > 10 {
			ui.Text(c, clock(time.Duration(p.Pos)*time.Second)).FontSize(12).TextColor(colors.accent)
		} else if ep.Seconds > 0 {
			ui.Text(c, clock(time.Duration(ep.Seconds)*time.Second)).FontSize(12).TextColor(colors.faint)
		}
	})
	if b.Clicked() {
		a.playEpisode(d.Item, *ep, at)
	}
}

// playView is the episode playing: it on the stage of the whole window, its
// controls floating over it and stepping out of the way, its drawer of
// episodes at hand.
func (a *app) playView(c *ui.Context) {
	playing := a.playing
	ui.Box(c).Fill().Background(ui.RGB(0, 0, 0)).Children(func() {
		// The stage answers the pointer: a move brings the controls, a click
		// plays or pauses, a double click takes the whole screen.
		stage := ui.ButtonBase(c.Key("stage")).Fill().Center().FocusRing(false).Label(i18n.T("Video"))
		stage.Background(ui.Transparent)
		stage.Children(func() {
			a.stageContent(c, playing)
		})
		if stage.Clicked() {
			if a.controlsOn {
				a.playPause()
			}
			a.wakeControls()
		}
		if stage.DoubleClicked() {
			if a.player != nil {
				a.toggleFullscreen()
			}
		}
		stage.HandleInput(func(ev ui.InputEvent) bool {
			if ev.Kind == ui.InputPointerMove && time.Since(a.controlsAt) > 250*time.Millisecond {
				a.wakeControls()
			}
			return false
		})
		a.playOverlays(c, playing)
	})
}

// stageContent is what the stage holds: the episode itself, or what stands
// in for it while it can't.
func (a *app) stageContent(c *ui.Context, playing playing) {
	switch {
	case a.pipActive():
		ui.Column(c).Gap(12).Center().Children(func() {
			ui.Icon(c, iconPip).Size(28, 28).TextColor(ui.RGBA(255, 255, 255, 0.35))
			ui.Text(c, i18n.T("Playing in the mini window")).FontSize(13).TextColor(ui.RGBA(255, 255, 255, 0.55))
			if ui.Button(c.Key("back-main"), i18n.T("Back to Main Window")).Clicked() {
				a.closePip()
			}
		})
	case a.deviceOutput && a.codie != nil:
		// The picture is on the device's screen; this is the now-playing
		// card that says where things stand: the drama's picture, what plays,
		// and how far along it is.
		ui.Column(c).Gap(18).Center().Children(func() {
			// The cover, round-cornered, with the drama's name and the
			// episode's beside it.
			ui.Row(c).Gap(16).AlignItems(ui.Center).MaxWidth(560).Children(func() {
				cover := ui.Box(c.Key("device-cover")).Width(96).Height(128).Radius(12).Background(colors.hover).Clip().Center()
				cover.Children(func() {
					if playing.item.Cover != "" {
						a.coverFill(c, playing.item.Cover)
					} else {
						ui.Icon(c, iconFilm).Size(28, 28).TextColor(colors.faint)
					}
				})
				ui.Column(c).Gap(6).Children(func() {
					ui.Text(c, playing.item.Title).FontSize(17).FontWeight(700).TextColor(ui.RGB(255, 255, 255)).MaxWidth(400).SingleLine()
					if ep := playing.ep.Title; ep != "" && ep != playing.item.Title {
						ui.Text(c, ep).FontSize(13).TextColor(ui.RGBA(255, 255, 255, 0.6)).MaxWidth(400).SingleLine()
					}
					if d := a.detail; d != nil && len(d.Episodes) > 0 {
						ui.Text(c, i18n.Tf("Ep %d of %d", playing.at+1, len(d.Episodes))).FontSize(12).TextColor(ui.RGBA(255, 255, 255, 0.45))
					}
				})
			})
			switch {
			case a.playerErr != "":
				ui.Text(c, a.playerErr).TextColor(ui.RGB(255, 255, 255)).FontSize(13).TextAlign(ui.Center).MaxWidth(420)
				if ui.Button(c.Key("retry"), i18n.T("Retry")).Clicked() {
					a.playEpisode(playing.item, playing.ep, playing.at)
				}
			case a.codieBusy != "":
				ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
					ui.Spinner(c).TextColor(ui.RGBA(255, 255, 255, 0.7))
					ui.Text(c, a.codieBusy).FontSize(13).TextColor(ui.RGBA(255, 255, 255, 0.7))
				})
			default:
				// What the device is doing with the episode, and how far it is.
				state := i18n.T("Playing on the codie device’s screen")
				if st := a.codieState; st != nil && !st.IsPlaying {
					state = i18n.T("Paused on the codie device’s screen")
				}
				ui.Column(c).Gap(6).AlignItems(ui.Center).Children(func() {
					ui.Text(c, state).FontSize(13).TextColor(ui.RGBA(255, 255, 255, 0.55))
					if s := a.codiePlayedOf(); s != "" {
						ui.Text(c, s).FontSize(15).FontWeight(600).TextColor(ui.RGBA(255, 255, 255, 0.8))
					}
				})
			}
		})
	default:
		player := a.player
		switch {
		case player != nil:
			ui.Video(c, player).Fill().Fit(ui.Contain)
			if player.Ready() && player.Buffering() {
				ui.Row(c).Absolute().Top(12).Right(14).Children(func() {
					ui.Spinner(c).Size(16, 16).TextColor(ui.RGBA(255, 255, 255, 0.6))
				})
			}
		case a.playerErr != "":
			ui.Column(c).Gap(12).Center().Children(func() {
				ui.Text(c, a.playerErr).TextColor(ui.RGB(255, 255, 255)).FontSize(13).TextAlign(ui.Center).MaxWidth(420)
				if ui.Button(c.Key("retry"), i18n.T("Retry")).Clicked() {
					a.playEpisode(playing.item, playing.ep, playing.at)
				}
			})
		}
	}
	// While the episode opens, its spinner floats centered over the stage,
	// wherever the video element sits.
	if opening, _ := a.openingState(); opening {
		ui.Box(c).Absolute().Top(0).Left(0).Right(0).Bottom(0).Children(func() {
			ui.Column(c).Fill().Justify(ui.Center).AlignItems(ui.Center).Children(func() {
				a.opening(c)
			})
		})
	}
}

// openingState reports whether the stage is still waiting for its episode,
// and whether anything at all is on it.
func (a *app) openingState() (bool, bool) {
	if a.pipActive() || (a.deviceOutput && a.codie != nil) || a.playerErr != "" {
		return false, false
	}
	if p := a.player; p != nil {
		return !p.Ready(), true
	}
	return true, false
}

// playOverlays is what floats over the stage: the countdown to the next
// episode, the drawer of episodes, and the controls.
func (a *app) playOverlays(c *ui.Context, playing playing) {
	// The episode ended: the next one is counted in.
	if p := a.player; p != nil && p.Ready() && p.Err() == nil && p.Ended() {
		if !a.nextLatch {
			a.nextLatch = true
			a.startNextCountdown()
		}
	}
	if a.nextLeft > 0 {
		ui.Box(c).Absolute().Top(52).Left(0).Right(0).AlignItems(ui.Center).Children(func() {
			banner := ui.Row(c).Gap(12).AlignItems(ui.Center).Padding(10, 16).Radius(999).
				Background(ui.RGBA(0, 0, 0, 0.72)).Border(1, ui.RGBA(255, 255, 255, 0.15))
			banner.Children(func() {
				if d := a.detail; d != nil && a.playing.at+1 < len(d.Episodes) {
					ui.Text(c, i18n.Tf("Up next: %s", d.Episodes[a.playing.at+1].Title)).FontSize(13).TextColor(ui.RGB(255, 255, 255)).SingleLine()
				} else {
					ui.Text(c, i18n.T("Up next")).FontSize(13).TextColor(ui.RGB(255, 255, 255))
				}
				ui.Text(c, i18n.Tf("%d s", a.nextLeft)).FontSize(13).TextColor(colors.muted)
				if ui.Button(c.Key("next-now"), i18n.T("Play Now")).Clicked() {
					a.cancelNext()
					a.nextEpisode(false)
				}
				if ui.Button(c.Key("next-cancel"), i18n.T("Cancel")).Clicked() {
					a.cancelNext()
					a.win.Invalidate()
				}
			})
		})
	}
	if a.drawerOpen {
		a.drawer(c)
	}
	a.playTopBar(c, playing)
	a.controlsOverlay(c, playing)
}

// playTopBar is the strip floating at the stage's top: the way back and what
// plays, over a gradient the way the foot's controls sit on one. It fades
// with the controls, leaving the picture alone.
func (a *app) playTopBar(c *ui.Context, playing playing) {
	if a.pipActive() {
		return
	}
	op := float32(1)
	if !a.controlsOn {
		op = 0
	}
	bar := ui.Box(c.Key("top-ctl")).Absolute().Top(0).Left(0).Right(0)
	opv := bar.AnimateTo("op", op, ui.Animation{Duration: 220 * time.Millisecond})
	bar.Opacity(opv).IgnoreInput(!a.controlsOn)
	bar.Children(func() {
		ui.Box(c).Height(84).Children(func() {
			ui.Box(c).Absolute().Top(0).Left(0).Right(0).Bottom(0).Draw(func(p *ui.Painter, r ui.Rect) {
				p.FillGradient(r, ui.LinearGradient{
					From:  ui.RGBA(0, 0, 0, 0.72),
					To:    ui.RGBA(0, 0, 0, 0),
					Angle: 180,
				}, 0)
			})
			// The window's buttons sit over this strip's left end, which
			// clears them.
			lead := float32(72)
			if l := c.TitleBar().Left; l > 0 {
				lead = l
			}
			row := ui.Row(c).Height(44).Absolute().Top(0).Left(0).Right(0).
				Padding(0, 16, 0, lead+8).Gap(10).AlignItems(ui.Center).DragWindow()
			row.HandleInput(func(ev ui.InputEvent) bool {
				if ev.Kind == ui.InputPointerMove {
					a.wakeControls()
				}
				return false
			})
			row.Children(func() {
				if b := iconButton(c.Key("back"), iconBack, 16, ui.RGB(255, 255, 255), i18n.T("Back")); b.Clicked() {
					a.back()
				}
				ui.Text(c, playing.item.Title).FontSize(14.5).FontWeight(600).
					TextColor(ui.RGB(255, 255, 255)).SingleLine().Grow(1).Shrink(1).MinWidth(0)
				if ep := playing.ep.Title; ep != "" && ep != playing.item.Title {
					ui.Text(c, ep).FontSize(12.5).TextColor(ui.RGBA(255, 255, 255, 0.6)).Shrink(0).SingleLine()
				}
			})
		})
	})
}

// wizard is the first run's hello: what the app does, in three lines, gone
// for good once taken.
func (a *app) wizard(c *ui.Context) {
	veil := ui.ButtonBase(c.Key("wizard-veil")).Absolute().Top(0).Left(0).Right(0).Bottom(0).
		FocusRing(false).Label(i18n.T("Dismiss"))
	veil.Background(colors.veil)
	if veil.Clicked() {
		a.settings.WizardDone = true
		a.settings.save()
	}
	veil.Children(func() {
		card := ui.Column(c).Width(380).Gap(14).Padding(24).Radius(16).
			Background(colors.panel).Border(1, colors.panelBorder).
			Shadow(0, 24, 60, -12, ui.RGBA(0, 0, 0, 0.6))
		card.Center()
		card.Children(func() {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Icon(c, iconFilm).Size(18, 18).TextColor(colors.accent)
				ui.Text(c, i18n.T("Welcome to Codie Video")).FontSize(17).FontWeight(800)
			})
			for _, line := range []struct {
				icon *ui.SVG
				text string
			}{
				{iconSearch, i18n.T("Pick a source in the sidebar; searches go to every source at once")},
				{iconClock, i18n.T("What you watch lands in Continue Watching; Follow keeps a drama close")},
				{iconPip, i18n.T("Connect Device pairs a codie to play on; the mini window watches while you browse")},
			} {
				ui.Row(c).Gap(10).AlignItems(ui.Center).Justify(ui.Start).Children(func() {
					ui.Icon(c, line.icon).Size(15, 15).TextColor(colors.accent)
					ui.Text(c, line.text).FontSize(13).TextColor(colors.muted)
				})
			}
			start := ui.ButtonBase(c.Key("wizard-go")).Height(36).PaddingX(20).Radius(999).Center().
				FocusRing(false).Label(i18n.T("Start Watching")).Background(colors.accent).TextColor(colors.onAccent)
			start.Children(func() {
				ui.Text(c, i18n.T("Start Watching")).FontSize(13.5).FontWeight(700)
			})
			if start.Clicked() {
				a.settings.WizardDone = true
				a.settings.save()
			}
		})
	})
}

// drawer is the play page's panel of episodes (or the active playlist queue),
// at the right of the stage.
func (a *app) drawer(c *ui.Context) {
	d := a.detail
	hasEps := d != nil && len(d.Episodes) > 0
	hasQueue := len(a.queue) > 1
	if !hasEps && !hasQueue {
		return
	}
	playlistOnly := hasQueue && (!hasEps || len(d.Episodes) <= 1)
	veil := ui.ButtonBase(c.Key("drawer-veil")).Absolute().Top(0).Left(0).Right(0).Bottom(0).
		FocusRing(false).Label(i18n.T("Close Episodes"))
	veil.Background(ui.RGBA(0, 0, 0, 0.45))
	if veil.Clicked() {
		a.drawerOpen = false
		a.wakeControls()
	}
	veil.Children(func() {
		panel := ui.Column(c).Width(330).Absolute().Top(0).Right(0).Bottom(0).
			Background(ui.RGBA(16, 16, 18, 0.97)).Border(1, colors.hairline).Children(func() {
			ui.Row(c).Height(48).Shrink(0).Padding(0, 14).Gap(8).AlignItems(ui.Center).Children(func() {
				if playlistOnly {
					title := a.queueTitle
					if title == "" {
						title = i18n.T("Playlist")
					}
					ui.Text(c, title).FontSize(15).FontWeight(700).Grow(1).SingleLine()
					ui.Text(c, i18n.Tf("%d videos", len(a.queue))).FontSize(12).TextColor(colors.muted)
				} else {
					ui.Text(c, i18n.T("Episodes")).FontSize(15).FontWeight(700).Grow(1)
					ui.Text(c, i18n.Tf("%d episodes", len(d.Episodes))).FontSize(12).TextColor(colors.muted)
				}
				if b := iconButton(c.Key("drawer-close"), iconX, 13, colors.muted, i18n.T("Close Episodes")); b.Clicked() {
					a.drawerOpen = false
					a.wakeControls()
				}
			})
			ui.Box(c).Height(1).Shrink(0).Background(colors.hairline)
			if playlistOnly {
				if len(a.queue) > 60 {
					ui.List(c.Key("drawer-qlist"), &a.queueList, len(a.queue), func(i int) {
						a.queueRow(c, i)
					}).Grow(1).Padding(8, 8, 12, 8)
				} else {
					ui.Scroll(c.Key("drawer-qscroll")).Grow(1).Padding(8, 8, 12, 8).Children(func() {
						ui.Column(c).Fill().Gap(4).Children(func() {
							for i := range a.queue {
								a.queueRow(c, i)
							}
						})
					})
				}
				return
			}
			p := a.lib.ProgressOf(d.ID)
			if len(d.Episodes) > 60 {
				ui.List(c.Key("drawer-list"), &a.epsList, len(d.Episodes), func(i int) {
					a.episodeRow(c, d, &d.Episodes[i], i, p)
				}).Grow(1).Padding(8, 8, 12, 8)
			} else {
				ui.Scroll(c.Key("drawer-scroll")).Grow(1).Padding(8, 8, 12, 8).Children(func() {
					ui.Column(c).Fill().Gap(4).Children(func() {
						for i := range d.Episodes {
							a.episodeRow(c, d, &d.Episodes[i], i, p)
						}
						if hasQueue {
							ui.Box(c).Height(1).Margin(8, 0, 6, 0).Background(colors.hairline)
							qtitle := a.queueTitle
							if qtitle == "" {
								qtitle = i18n.T("Playlist")
							}
							ui.Text(c, qtitle).FontSize(12.5).FontWeight(700).TextColor(colors.muted).Padding(0, 4, 4, 4)
							for i := range a.queue {
								a.queueRow(c, i)
							}
						}
					})
				})
			}
		})
		_ = panel
	})
}

// queueRow is one video of the active playlist queue inside the player drawer.
func (a *app) queueRow(c *ui.Context, idx int) {
	if idx < 0 || idx >= len(a.queue) {
		return
	}
	it := a.queue[idx]
	nowPlaying := a.queueAt == idx && a.playing.item.ID == it.ID
	label := strconv.Itoa(idx+1) + "  " + it.Title
	b := ui.ButtonBase(c.Key("qr" + it.ID + ":" + strconv.Itoa(idx))).Height(38).PaddingX(12).Radius(8).
		AlignItems(ui.Center).Justify(ui.Start).Gap(8).FocusRing(false).Label(label)
	switch {
	case nowPlaying:
		b.Background(colors.accent.Alpha(0.25)).Border(1, colors.accent.Alpha(0.6))
	case b.Hovered():
		b.Background(colors.hover)
	default:
		b.Background(colors.press)
	}
	b.Children(func() {
		t := ui.Text(c, label).FontSize(13).Grow(1).Shrink(1).MinWidth(0).SingleLine()
		if nowPlaying {
			t.TextColor(colors.accent).FontWeight(600)
			ui.Icon(c, iconPlay).Size(12, 12).TextColor(colors.accent)
		}
	})
	if b.Clicked() {
		a.playQueueAt(idx)
	}
}

// controlsOverlay is the strip floating at the stage's foot: its name, how
// far it is, and the buttons of playing it. It steps out of the way while
// the episode plays and the pointer sits still.
func (a *app) controlsOverlay(c *ui.Context, playing playing) {
	if a.pipActive() {
		return
	}
	op := float32(1)
	if !a.controlsOn {
		op = 0
	}
	ctl := ui.Box(c.Key("ctl")).Absolute().Left(0).Right(0).Bottom(0)
	opv := ctl.AnimateTo("op", op, ui.Animation{Duration: 220 * time.Millisecond})
	ctl.Opacity(opv).IgnoreInput(!a.controlsOn)
	ctl.Children(func() {
		ui.Column(c).Fill().Children(func() {
			// The gradient the controls sit on, so they read over anything.
			ui.Box(c).Height(70).Shrink(0).Draw(func(p *ui.Painter, r ui.Rect) {
				p.FillGradient(r, ui.LinearGradient{
					From:  ui.RGBA(0, 0, 0, 0),
					To:    ui.RGBA(0, 0, 0, 0.72),
					Angle: 180,
				}, 0)
			})
			ui.Column(c).Shrink(0).Padding(0, 16, 12, 16).Gap(8).Background(ui.RGBA(0, 0, 0, 0.72)).Children(func() {
				a.controlsBody(c, playing)
			})
		})
	})
}

// controlsBody is the controls' content, floating or docked alike. The stage
// under it is dark in both palettes, so its own colors are whites.
func (a *app) controlsBody(c *ui.Context, playing playing) {
	// The name of what plays: the drama, then the episode when it says
	// something the drama's name doesn't.
	ui.Row(c).Gap(8).AlignItems(ui.Baseline).Children(func() {
		ui.Text(c, playing.item.Title).FontSize(15).FontWeight(600).TextColor(ui.RGB(255, 255, 255)).Grow(1).Shrink(1).MinWidth(0).SingleLine()
		ep := playing.ep.Title
		if ep != "" && ep != playing.item.Title {
			ui.Text(c, ep).FontSize(12.5).TextColor(ui.RGBA(255, 255, 255, 0.6)).Shrink(0).SingleLine()
		}
		if a.nextLeft > 0 {
			ui.Text(c, i18n.Tf("Next in %d s", a.nextLeft)).FontSize(12).TextColor(colors.accent).Shrink(0)
		}
	})
	if a.deviceOutput && a.codie != nil && (a.codieBusy != "" || a.playerErr != "") {
		ui.Text(c, a.codieStatus()).FontSize(12.5).TextColor(ui.RGBA(255, 255, 255, 0.6)).SingleLine()
	}
	// How far the episode is, with the clock at both of its ends.
	ui.Row(c).Height(20).Gap(10).AlignItems(ui.Center).Children(func() {
		pos, dur := a.stageClocks()
		at, whole := "", ""
		if dur > 0 {
			at, whole = clock(pos), clock(dur)
		}
		ui.Text(c, at).FontSize(11.5).TextColor(ui.RGBA(255, 255, 255, 0.6)).Shrink(0)
		ui.Box(c).Grow(1).MinWidth(0).Children(func() {
			if a.deviceOutput && a.codie != nil {
				a.codieScrubber(c)
			} else if player := a.player; player != nil && player.Ready() && player.Err() == nil {
				a.scrubber(c, player)
			} else {
				a.idleBar(c)
			}
		})
		ui.Text(c, whole).FontSize(11.5).TextColor(ui.RGBA(255, 255, 255, 0.6)).Shrink(0)
	})
	ui.Row(c).Height(38).Gap(6).AlignItems(ui.Center).Children(func() {
		d := a.detail
		if a.hasEpisode(-1) {
			if b := iconButton(c.Key("prev"), iconPrev, 15, ui.RGB(255, 255, 255), i18n.T("Previous Episode")); b.Clicked() {
				a.nextEpisode(true)
			}
		}
		if a.deviceOutput && a.codie != nil {
			icon := iconPlay
			if a.codieState != nil && a.codieState.IsPlaying {
				icon = iconPause
			}
			if b := iconButton(c.Key("play"), icon, 17, ui.RGB(255, 255, 255), i18n.T("Play/Pause")); b.Clicked() {
				a.codiePlayPause()
			}
		} else if player := a.player; player != nil {
			icon := iconPlay
			if player.Playing() {
				icon = iconPause
			}
			if b := iconButton(c.Key("play"), icon, 17, ui.RGB(255, 255, 255), i18n.T("Play/Pause")); b.Clicked() {
				a.playPause()
			}
		} else {
			iconButton(c.Key("play"), iconPlay, 17, ui.RGBA(255, 255, 255, 0.35), i18n.T("Play/Pause"))
		}
		if a.hasEpisode(1) {
			if b := iconButton(c.Key("next"), iconNext, 15, ui.RGB(255, 255, 255), i18n.T("Next Episode")); b.Clicked() {
				a.cancelNext()
				a.nextEpisode(false)
			}
		}
		ui.Spacer(c)
		// Episodes / Playlist: one press, and the drawer of episodes or the
		// active playlist queue comes.
		if (d != nil && len(d.Episodes) > 0) || len(a.queue) > 1 {
			drawerLabel := i18n.T("Episodes")
			if len(a.queue) > 1 && (d == nil || len(d.Episodes) <= 1) {
				drawerLabel = i18n.T("Playlist")
			}
			b := ui.ButtonBase(c.Key("drawer")).Height(30).PaddingX(12).Radius(999).Center().Gap(6).
				FocusRing(false).Label(drawerLabel)
			if a.drawerOpen {
				b.Background(ui.RGBA(255, 255, 255, 0.16))
			}
			b.Children(func() {
				ui.Icon(c, iconList).Size(13, 13).TextColor(ui.RGB(255, 255, 255))
				ui.Text(c, drawerLabel).FontSize(12.5).FontWeight(600).TextColor(ui.RGB(255, 255, 255))
			})
			if b.Clicked() {
				a.drawerOpen = !a.drawerOpen
				a.speedOpen = false
				a.wakeControls()
			}
		}
		// Speed: one press, and the rates float up over it. On the device a
		// change makes the episode again at the new pace, the file taking a
		// while to make.
		if a.deviceOutput || a.player != nil {
			a.speedControl(c)
		}
		// Mini Player: watch on while browsing.
		if !a.deviceOutput && a.player != nil {
			if b := iconButton(c.Key("pip"), iconPip, 15, ui.RGB(255, 255, 255), i18n.T("Mini Player")); b.Clicked() {
				a.openPip()
			}
		}
		// Fullscreen: the whole screen is the picture.
		fs := iconMaximize
		if a.fullscreen {
			fs = iconRestore
		}
		if b := iconButton(c.Key("fs"), fs, 15, ui.RGB(255, 255, 255), i18n.T("Fullscreen")); b.Clicked() {
			a.toggleFullscreen()
		}
		if a.deviceOutput && a.codie != nil {
			// The stage card says how far the episode is; here only the
			// loudness, which is the device's, even while the episode is still
			// being made for it.
			a.codieVolumeControl(c)
		} else if player := a.player; player != nil {
			a.volume(c, player)
		} else if a.deviceOutput {
			// Chosen for the device before it's paired: the slider still says
			// how loud the device will play.
			a.codieVolumeControl(c)
		}
		// Where the picture plays: paired, the toggle rides along, local or
		// on the device alike.
		if a.codie != nil {
			a.outputToggle(c)
		}
	})
}

// speedControl is the button of how fast, with the choices floating over it
// when taken.
func (a *app) speedControl(c *ui.Context) {
	b := ui.ButtonBase(c.Key("speed")).Height(30).PaddingX(10).Radius(999).Center().Gap(5).
		FocusRing(false).Label(i18n.T("Speed"))
	if a.speedOpen {
		b.Background(colors.hover)
	}
	b.Children(func() {
		ui.Icon(c, iconSpeed).Size(13, 13).TextColor(ui.RGB(255, 255, 255))
		ui.Text(c, rateLabel(a.rate)).FontSize(12.5).FontWeight(600).TextColor(ui.RGB(255, 255, 255))
	})
	if b.Clicked() {
		a.speedOpen = !a.speedOpen
		a.wakeControls()
	}
	if a.speedOpen {
		ui.Column(c.Key("speed-pop")).Width(96).Padding(5).Gap(1).Radius(10).
			Absolute().Bottom(58).Right(60).
			Background(ui.Hex("#1e1e22")).Border(1, ui.RGBA(255, 255, 255, 0.12)).
			Shadow(0, 12, 30, -8, ui.RGBA(0, 0, 0, 0.55)).Children(func() {
			for _, r := range []float64{2, 1.5, 1.25, 1, 0.75, 0.5} {
				chosen := a.rate == r
				b := ui.ButtonBase(c.Key("r" + rateLabel(r))).Height(30).PaddingX(12).Radius(7).Center().
					Justify(ui.Center).FocusRing(false).Label(rateLabel(r))
				if chosen {
					b.Background(colors.accent.Alpha(0.22))
				} else if b.Hovered() {
					b.Background(ui.RGBA(255, 255, 255, 0.08))
				}
				b.Children(func() {
					t := ui.Text(c, rateLabel(r)).FontSize(13)
					if chosen {
						t.TextColor(colors.accent).FontWeight(700)
					} else {
						t.TextColor(ui.RGB(255, 255, 255))
					}
				})
				if b.Clicked() {
					a.setRate(r)
					a.speedOpen = false
					a.wakeControls()
				}
			}
		})
	}
}

// rateLabel is a speed as the button says it.
func rateLabel(r float64) string {
	s := strconv.FormatFloat(r, 'f', -1, 64)
	return s + "×"
}

// idleBar is where the progress bar goes while there's nothing to measure.
func (a *app) idleBar(c *ui.Context) {
	ui.Box(c).Height(20).Children(func() {
		ui.Box(c).Absolute().Top(8).Left(0).Right(0).Height(4).Radius(2).Background(ui.RGBA(255, 255, 255, 0.18))
	})
}

// stageClocks is how far the episode is and how long it runs, on the stage —
// here or on the device alike.
func (a *app) stageClocks() (time.Duration, time.Duration) {
	if a.deviceOutput && a.codie != nil {
		return a.codieTimes()
	}
	if p := a.player; p != nil && p.Ready() && p.Err() == nil {
		return p.Position(), p.Duration()
	}
	return 0, 0
}

// hasEpisode reports whether there's an episode (or a next/previous video in
// the active playlist queue) to go to, back or forward by dir.
func (a *app) hasEpisode(dir int) bool {
	if d := a.detail; d != nil {
		at := a.playing.at + dir
		if at >= 0 && at < len(d.Episodes) {
			return true
		}
	}
	if len(a.queue) > 1 {
		qat := a.queueAt + dir
		return qat >= 0 && qat < len(a.queue)
	}
	return false
}

// scrubber is how far the episode is, which seeks where it is dragged or
// clicked, once the pointer is off it.
func (a *app) scrubber(c *ui.Context, player *ui.VideoPlayer) {
	st := &a.scrub
	dur := player.Duration().Seconds()
	if !st.active && !st.held {
		st.v = player.Position().Seconds()
	}
	s := ui.SliderBase(c, &st.v, 0, max(dur, 1)).Height(20).FocusRing(false).Label(i18n.T("Progress")).Cursor(ui.CursorPointer)
	s.HandleInput(func(ev ui.InputEvent) bool {
		switch ev.Kind {
		case ui.InputPointerDown:
			st.held = ev.Button == 0
			a.cancelNext()
		case ui.InputPointerUp:
			st.held = false
		}
		return false
	})
	seek := func() {
		player.Seek(time.Duration(min(st.v, max(0, dur-1)) * float64(time.Second)))
		a.cancelNext()
		a.wakeControls()
	}
	switch {
	case st.held:
		st.active = true
	case st.active:
		st.active = false
		seek()
	case s.Changed():
		seek()
	}
	at := st.v
	if !st.active {
		at = player.Position().Seconds()
	}
	frac := float32(0)
	if dur > 0 {
		frac = float32(min(1, max(0, at/dur)))
	}
	thick := float32(4)
	if s.Hovered() || st.held {
		thick = 6
	}
	thick = s.AnimateTo("thick", thick, ui.Snappy)
	s.Draw(func(p *ui.Painter, r ui.Rect) {
		y := r.Y + (r.H-thick)/2
		p.Fill(ui.Rect{X: r.X, Y: y, W: r.W, H: thick}, ui.RGBA(255, 255, 255, 0.18), thick/2)
		p.Fill(ui.Rect{X: r.X, Y: y, W: max(thick, r.W*frac), H: thick}, colors.scrub, thick/2)
	})
}

// volume is how loud the episode is, its icon saying whether it's muted. The
// app keeps the level from episode to episode, and from run to run.
func (a *app) volume(c *ui.Context, player *ui.VideoPlayer) {
	if player == nil {
		return
	}
	icon := iconVolume
	if player.Muted() || a.vol == 0 {
		icon = iconMute
	} else if a.vol < 50 {
		icon = iconVolumeLow
	}
	if b := iconButton(c.Key("mute"), icon, 14, ui.RGBA(255, 255, 255, 0.6), i18n.T("Mute")); b.Clicked() {
		player.SetMuted(!player.Muted())
	}
	sl := ui.Slider(c.Key("vol"), &a.vol, 0, 100).Width(90)
	if sl.Changed() {
		a.setVolume(a.vol)
		player.SetVolume(a.vol / 100)
	}
}

// outputToggle is Local and codie, the two places an episode plays.
func (a *app) outputToggle(c *ui.Context) {
	if a.codie == nil {
		return
	}
	ui.Row(c).Shrink(0).Gap(2).Padding(2).Radius(999).Background(ui.RGBA(255, 255, 255, 0.08)).Children(func() {
		for _, o := range []struct {
			label  string
			device bool
		}{{"Local", false}, {"codie", true}} {
			on := a.deviceOutput == o.device
			b := ui.ButtonBase(c.Key(o.label)).Height(24).PaddingX(12).Radius(999).Center().FocusRing(false).Label(i18n.T(o.label))
			if on {
				b.Background(ui.RGBA(255, 255, 255, 0.22)).TextColor(ui.RGB(255, 255, 255))
			} else {
				b.Background(ui.Transparent).TextColor(ui.RGBA(255, 255, 255, 0.72))
				if b.Hovered() {
					b.Background(ui.RGBA(255, 255, 255, 0.10)).TextColor(ui.RGB(255, 255, 255))
				}
			}
			b.Children(func() { ui.Text(c, i18n.T(o.label)).FontSize(12.5).FontWeight(600) })
			if b.Clicked() {
				a.codieToggle(o.device)
			}
		}
	})
}

// codieScrubber is how far the episode is on the device, which seeks there.
func (a *app) codieScrubber(c *ui.Context) {
	st := &a.scrub
	pos, dur := a.codieTimes()
	secs := dur.Seconds()
	if !st.active && !st.held {
		st.v = pos.Seconds()
	}
	s := ui.SliderBase(c, &st.v, 0, max(secs, 1)).Height(20).FocusRing(false).Label(i18n.T("Progress")).Cursor(ui.CursorPointer)
	s.HandleInput(func(ev ui.InputEvent) bool {
		switch ev.Kind {
		case ui.InputPointerDown:
			st.held = ev.Button == 0
		case ui.InputPointerUp:
			st.held = false
		}
		return false
	})
	switch {
	case st.held:
		st.active = true
	case st.active:
		st.active = false
		a.codieSeek(int64(min(st.v, max(0, secs-1)) * 1000))
	case s.Changed():
		a.codieSeek(int64(min(st.v, max(0, secs-1)) * 1000))
	}
	at := st.v
	if !st.active {
		at = pos.Seconds()
	}
	frac := float32(0)
	if secs > 0 {
		frac = float32(min(1, max(0, at/secs)))
	}
	thick := float32(4)
	if s.Hovered() || st.held {
		thick = 6
	}
	thick = s.AnimateTo("thick", thick, ui.Snappy)
	s.Draw(func(p *ui.Painter, r ui.Rect) {
		y := r.Y + (r.H-thick)/2
		p.Fill(ui.Rect{X: r.X, Y: y, W: r.W, H: thick}, ui.RGBA(255, 255, 255, 0.18), thick/2)
		p.Fill(ui.Rect{X: r.X, Y: y, W: max(thick, r.W*frac), H: thick}, colors.scrub, thick/2)
	})
}

// codieTimes is how far and how long the episode is on the device, across
// all split segments when an oversized episode was broken into pieces.
func (a *app) codieTimes() (time.Duration, time.Duration) {
	st := a.codieState
	if st == nil {
		return 0, 0
	}
	posMS := st.PositionMS
	durMS := st.DurationMS
	if len(a.codieSegs) > 1 && a.codieSegAt >= 0 && a.codieSegAt < len(a.codieSegs) {
		posMS += a.codieSegs[a.codieSegAt].startMS
		if a.codieTotalMS > 0 {
			durMS = a.codieTotalMS
		}
	} else if a.codieTotalMS > durMS {
		durMS = a.codieTotalMS
	}
	return time.Duration(posMS) * time.Millisecond, time.Duration(durMS) * time.Millisecond
}

// codiePlayedOf is the device episode's time, as the controls say it.
func (a *app) codiePlayedOf() string {
	pos, dur := a.codieTimes()
	if dur <= 0 {
		return ""
	}
	return clock(pos) + " / " + clock(dur)
}

// codieVolumeControl is how loud the device plays.
func (a *app) codieVolumeControl(c *ui.Context) {
	icon := iconVolume
	if a.vol == 0 {
		icon = iconMute
	} else if a.vol < 50 {
		icon = iconVolumeLow
	}
	if b := iconButton(c.Key("mute"), icon, 14, ui.RGBA(255, 255, 255, 0.6), i18n.T("Mute")); b.Clicked() {
		a.vol = map[bool]float64{true: 0, false: 100}[a.vol == 0]
		a.setVolume(a.vol)
		a.codieVolume(a.vol / 100)
	}
	sl := ui.Slider(c.Key("vol"), &a.vol, 0, 100).Width(90)
	if sl.Changed() {
		a.setVolume(a.vol)
		a.codieVolume(a.vol / 100)
	}
}

// iconButton is a borderless button showing an icon, which a soft round
// fill marks under the pointer.
func iconButton(c *ui.Context, icon *ui.SVG, size float32, color ui.Color, label string) ui.Element {
	b := ui.ButtonBase(c).Size(size+16, size+16).Center().Radius(999).Label(label).TextColor(color)
	bg := ui.Transparent
	if b.Hovered() {
		bg = ui.RGBA(255, 255, 255, 0.1)
	}
	b.Background(b.AnimateColor("bg", bg, ui.Animation{Duration: 120 * time.Millisecond}))
	pose := ui.Pose{}
	if b.Pressed() {
		pose.Scale = 0.88
	}
	b.AnimatePose(pose, ui.Snappy)
	b.Children(func() { ui.Icon(c, icon).Size(size, size) })
	return b
}

// opening is the while of an episode on its way to the stage.
func (a *app) opening(c *ui.Context) {
	ui.Row(c).Gap(10).Center().Children(func() {
		ui.Spinner(c).TextColor(ui.RGBA(255, 255, 255, 0.7))
		ui.Text(c, i18n.T("Opening…")).FontSize(13).TextColor(ui.RGBA(255, 255, 255, 0.7))
	})
}

// loading is the while of a page not yet there.
func (a *app) loading(c *ui.Context) {
	ui.Row(c).Fill().Justify(ui.Center).AlignItems(ui.Center).Padding(60, 0, 0, 0).Children(func() {
		ui.Spinner(c).Size(18, 18).Margin(0, 8, 0, 0)
		ui.Text(c, i18n.T("Loading…")).FontSize(13).TextColor(colors.muted)
	})
}

// centered says what went wrong, or what there is to say, with a button to
// try again when there's a try to make.
func (a *app) centered(c *ui.Context, msg, retry string, fn func()) {
	ui.Column(c).Fill().Center().Gap(12).Children(func() {
		ui.Text(c, msg).TextColor(colors.muted).FontSize(13).TextAlign(ui.Center).MaxWidth(520)
		if fn != nil && retry != "" {
			if ui.Button(c.Key("retry"), retry).Clicked() {
				fn()
			}
		}
	})
}

// columns is how many cards of at least min points wide a row here holds.
func columns(c *ui.Context, min float32) int {
	w, _ := c.Size()
	if w <= 0 {
		return 4
	}
	n := int(w / min)
	if n < 2 {
		n = 2
	}
	return n
}

// itoa is a number as the page says it.
func itoa(n int) string { return strconv.Itoa(n) }
