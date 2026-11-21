package app

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"codie-video-player/internal/video"
)

// The window: a dark page whose picture takes the whole stage, its controls
// floating over it and stepping out of the way, a drama's episodes at hand
// in a drawer. Everything is the app's own drawing; the window's buttons sit
// over the top bar.

// colors are the page's, dark as a cinema's.
var colors = struct {
	bg, text, muted, faint, hairline, hover, press, accent, onAccent, scrub ui.Color
}{
	bg:       ui.Hex("#141416"),
	text:     ui.Hex("#f5f5f7"),
	muted:    ui.RGBA(235, 235, 245, 0.56),
	faint:    ui.RGBA(235, 235, 245, 0.3),
	hairline: ui.RGBA(255, 255, 255, 0.08),
	hover:    ui.RGBA(255, 255, 255, 0.06),
	press:    ui.RGBA(255, 255, 255, 0.1),
	accent:   ui.Oklch(0.62, 0.21, 20),
	onAccent: ui.Hex("#ffffff"),
	scrub:    ui.Oklch(0.66, 0.16, 25),
}

// topBarHeight is the least height of the strip along the top.
const topBarHeight = 40

// view is the content of the window.
func (a *app) view(c *ui.Context) {
	t := *c.Theme()
	t.Dark = true
	t.Accent, t.AccentHover, t.AccentPressed = colors.accent, colors.accent, colors.accent
	c.SetTheme(&t)
	root := c.Root()
	root.Background(colors.bg)

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
		case a.page != pageList:
			a.back()
		}
	})
	root.OnShortcut(0, ui.KeyLeft, func() { a.shortcutSeek(-5 * time.Second) })
	root.OnShortcut(0, ui.KeyRight, func() { a.shortcutSeek(5 * time.Second) })
	root.OnShortcut(0, ui.KeyUp, func() { a.shortcutVolume(5) })
	root.OnShortcut(0, ui.KeyDown, func() { a.shortcutVolume(-5) })
	root.OnShortcut(0, ui.KeyF, func() {
		if a.page == pagePlay && a.player != nil {
			a.toggleFullscreen()
		}
	})
	root.OnShortcut(ui.Cmd, ui.KeyF, func() {
		if a.page == pageList {
			a.searchBox.Focus(c)
		}
	})

	ui.Column(c).Fill().Children(func() {
		a.topBar(c)
		// A page fades in as it opens, keyed by which page and of what.
		id := fmt.Sprintf("page:%d:%s", a.page, a.detailOf)
		page := ui.Box(c.Key(id)).Grow(1).MinHeight(0)
		op := page.AnimateTo("fade", 1, ui.Animation{Duration: 160 * time.Millisecond})
		page.Children(func() {
			ui.Column(c).Fill().Opacity(op).Children(func() {
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
}

// shortcutSeek moves the episode on by d, when one is on the stage.
func (a *app) shortcutSeek(d time.Duration) {
	if a.page == pagePlay && !a.pipActive() && !a.deviceOutput {
		a.seekBy(d)
	}
}

// shortcutVolume raises or lowers how loud, when the stage plays.
func (a *app) shortcutVolume(d float64) {
	if a.page == pagePlay && !a.pipActive() && !a.deviceOutput {
		a.bumpVolume(d)
	}
}

// topBar is the strip the window buttons sit over: back, forward, the name,
// and the search field. It drags the window where nothing takes the press.
func (a *app) topBar(c *ui.Context) {
	ui.Row(c).Height(topBarHeight).Padding(12, 16, 4, 16).Gap(12).AlignItems(ui.Center).DragWindow().Children(func() {
		if a.page != pageList {
			if b := iconButton(c.Key("back"), iconBack, 16, colors.text, "返回"); b.Clicked() {
				a.back()
			}
		}
		if len(a.fwd) > 0 && a.page == pageList {
			if b := iconButton(c.Key("forward"), iconForward, 16, colors.text, "前进"); b.Clicked() {
				a.forward()
			}
		}
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, iconFilm).Size(17, 17).TextColor(colors.accent)
			ui.Text(c, "Codie 视频").FontSize(15).FontWeight(700).SingleLine()
		})
		if a.page == pageList {
			ui.Spacer(c)
			a.sourcePicker(c)
		} else {
			ui.Spacer(c)
		}
		// 设置:齿轮上一枚红点,仓库有新版本时它在。
		gear := ui.ButtonBase(c.Key("settings")).Size(31, 31).Center().Radius(999).
			Label("设置").TextColor(colors.muted)
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
		// 连接设备:带一个点,连上之后它是绿的。
		b := ui.ButtonBase(c.Key("device")).Height(30).PaddingX(14).Radius(999).Gap(7).AlignItems(ui.Center).
			FocusRing(false).Label("连接设备").TextColor(colors.text)
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
			ui.Text(c, "连接设备").FontSize(13).FontWeight(600)
		})
		if b.Clicked() {
			a.openDeviceWindow()
		}
		if a.page == pageList {
			a.searchField(c)
		}
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
	if ui.Select(c.Key("source"), &chosen, names).Width(150).Changed() {
		for i, n := range names {
			if n == chosen {
				a.useSource(a.sources[i].id)
			}
		}
	}
}

// searchField is what a search is typed into, which searches every source
// where Enter is pressed, and clears by its cross. Under it, while the field
// holds nothing and the focus sits in it, are the last queries.
func (a *app) searchField(c *ui.Context) {
	field := ui.Row(c.Key("search-field")).Width(280).Height(30).Radius(8).AlignItems(ui.Stretch)
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
		in := ui.TextInputBase(c.Key("query"), &a.query).Bind(&a.searchBox).Grow(1).MinWidth(0).
			PaddingStart(29).PaddingEnd(end).Placeholder("搜短剧、漫剧、视频").FontSize(13).Label("搜索")
		if in.Submitted() {
			a.searchAll(a.query)
		}
		ui.Icon(c, iconSearch).Size(13, 13).TextColor(colors.muted).Absolute().Left(9).Top(7.5).IgnoreInput(true)
		if clearable {
			x := ui.ButtonBase(c.Key("clear")).Size(22, 22).Center().Absolute().Right(4).Top(4).
				KeepFocus().FocusRing(false).Label("清除搜索")
			if x.Clicked() {
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
	// The last queries, while the field is empty and focused.
	if c.Focused(a.searchBox) && a.query == "" && len(a.settings.SearchHistory) > 0 {
		ui.Column(c.Key("search-history")).Width(280).AlignItems(ui.Stretch).
			Absolute().Top(34).Right(0).Padding(6).Gap(2).
			Background(ui.Hex("#1e1e22")).Radius(10).Border(1, colors.hairline).
			Shadow(0, 10, 30, -8, ui.RGBA(0, 0, 0, 0.5)).Children(func() {
			for _, s := range a.settings.SearchHistory {
				h := ui.ButtonBase(c.Key("h"+s)).Height(28).PaddingX(10).Radius(6).AlignItems(ui.Center).
					Justify(ui.Start).Gap(8).FocusRing(false).Label("搜索 "+s)
				if h.Hovered() {
					h.Background(colors.hover)
				}
				h.Children(func() {
					ui.Icon(c, iconClock).Size(12, 12).TextColor(colors.faint)
					ui.Text(c, s).FontSize(12.5).TextColor(colors.muted).SingleLine()
				})
				if h.Clicked() {
					a.query = s
					a.searchAll(s)
				}
			}
		})
	}
}

// listView is the chips over the grid of what the tab lists, the viewer's
// own shelves above them, and a search's answers in place of it all.
func (a *app) listView(c *ui.Context) {
	ui.Column(c).Fill().Children(func() {
		if e := a.sourceEntry; e != nil && e.grant != nil {
			ui.Row(c).Shrink(0).Padding(0, 20, 10, 20).Gap(10).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "这个插件要启动 ffmpeg / yt-dlp 才能取流。").FontSize(13).TextColor(colors.muted).Grow(1)
				if ui.Button(c.Key("grant"), "允许").Clicked() {
					a.grantSource(e.id)
				}
			})
		}
		a.chips(c)
		ui.Scroll(c.Key("grid")).Fill().Padding(10, 20, 32, 20).Children(func() {
			if a.searching {
				a.searchView(c)
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

// searchView is what a search found: a chip to a source that had any, over
// their answers together.
func (a *app) searchView(c *ui.Context) {
	if len(a.searchGroups) > 1 {
		ui.Row(c).Shrink(0).Padding(0, 0, 12, 0).Gap(8).AlignItems(ui.Center).Children(func() {
			a.searchChip(c, "all", "全部", a.countSearchItems())
			for _, g := range a.searchGroups {
				if len(g.items) > 0 {
					a.searchChip(c, g.id, g.name, len(g.items))
				}
			}
		})
	}
	items := a.searchItems()
	probs := a.searchProblems()
	switch {
	case a.gridLoading && len(a.searchGroups) == 0:
		a.loading(c)
	case len(items) == 0 && probs != "" && a.countSearchHits() == 0:
		a.centered(c, probs, "再试一次", func() { a.searchAll(a.query) })
	case len(items) == 0 && len(a.searchGroups) > 0:
		a.centered(c, "没找到「"+a.query+"」", "", nil)
	case len(items) == 0:
		a.loading(c)
	default:
		ui.Grid(c).Columns(columns(c, 150)).GapX(16).GapY(24).Children(func() {
			for i := range items {
				a.card(c.Key("s"+items[i].ID+":"+strconv.Itoa(i)), &items[i])
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
	b := ui.ButtonBase(c.Key("f"+id)).Height(28).PaddingX(12).Radius(999).Center().Gap(6).
		FocusRing(false).Label("来源 "+name)
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
func (a *app) shelves(c *ui.Context) {
	if a.lib == nil {
		return
	}
	if len(a.lib.Follows) > 0 {
		a.shelf(c, "追剧", iconHeart, a.lib.Follows)
	}
	if len(a.lib.History) > 0 {
		a.shelf(c, "继续观看", iconClock, a.lib.History)
	}
}

// shelf is one of the viewer's rows: its name over a line of cards that
// scrolls sideways.
func (a *app) shelf(c *ui.Context, name string, icon *ui.SVG, entries []watched) {
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
		ui.ScrollHorizontal(c.Key("shelf-"+name)).Children(func() {
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
func (a *app) shelfCard(c *ui.Context, e watched) {
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
func shelfCaption(e watched) string {
	if e.Done {
		return "已看完"
	}
	at := e.EpAt + 1
	if e.Ep != "" {
		return "第" + strconv.Itoa(at) + "集 · " + e.Ep
	}
	return "第" + strconv.Itoa(at) + "集"
}

// resumeWatch takes a drama of the shelves up where it stopped.
func (a *app) resumeWatch(e watched) {
	ep := video.Episode{ID: e.EpID, Title: e.Ep}
	if e.EpID == "" {
		ep = video.Episode{}
	}
	a.playEpisode(e.Item, ep, e.EpAt)
}

// chips are the kinds of drama the source lists, one to each, the one
// showing raised.
func (a *app) chips(c *ui.Context) {
	if len(a.tabs) == 0 {
		return
	}
	ui.Row(c).Shrink(0).Padding(2, 20, 10, 20).Gap(8).Children(func() {
		for _, t := range a.tabs {
			a.chip(c.Key(t.ID), t, a.tabID == t.ID && !a.searching)
		}
	})
}

// chip is one of the kinds, raised when chosen.
func (a *app) chip(c *ui.Context, t video.Tab, chosen bool) {
	b := ui.ButtonBase(c).Height(30).PaddingX(14).Radius(999).Center().FocusRing(false).Label(t.Label)
	switch {
	case chosen:
		b.Background(colors.accent).TextColor(colors.onAccent)
	case b.Hovered():
		b.Background(colors.press)
	default:
		b.Background(colors.hover)
	}
	b.Children(func() {
		ui.Text(c, t.Label).FontSize(13).FontWeight(600).SingleLine()
	})
	if b.Clicked() {
		a.useTab(t.ID)
	}
}

// grid is the dramas as cards, so many to a row as fit, with what the page
// says while it loads, or why it didn't.
func (a *app) grid(c *ui.Context) {
	switch {
	case a.gridErr != "" && len(a.items) == 0:
		a.gridError(c, a.gridErr)
	case a.tabsErr != "" && len(a.items) == 0:
		a.gridError(c, a.tabsErr)
	case (a.gridLoading || a.tabsLoading) && len(a.items) == 0:
		a.skeleton(c)
	case len(a.items) == 0 && a.tabID != "":
		a.centered(c, "这里还没有内容", "", nil)
	case len(a.items) == 0:
		a.skeleton(c)
	default:
		a.cards(c)
		if a.gridLoading {
			ui.Row(c).Center().Padding(14, 0, 0, 0).Children(func() {
				ui.Spinner(c).Size(16, 16)
			})
		} else {
			ui.Row(c).Center().Padding(16, 0, 0, 0).Children(func() {
				if ui.Button(c.Key("more"), "加载更多").Clicked() {
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
			if ui.Button(c.Key("retry"), "重试").Clicked() {
				a.loadTabs()
				a.loadTab()
			}
			for i := range a.sources {
				e := &a.sources[i]
				if a.sourceEntry != nil && e.id == a.sourceEntry.id {
					continue
				}
				if ui.Button(c.Key("go"+e.id), "去"+e.name).Clicked() {
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
			ui.Column(c.Key("sk"+strconv.Itoa(i))).Gap(8).AlignItems(ui.Stretch).Children(func() {
				ui.Box(c).AspectRatio(3.0 / 4.0).Radius(10).Background(colors.hover)
				ui.Box(c).Height(13).Width(110).Radius(5).Background(colors.hover)
				ui.Box(c).Height(10).Width(70).Radius(5).Background(colors.hover)
			})
		}
	})
}

// cards is a page of dramas: a poster over its title and a line under that,
// opening the drama's page.
func (a *app) cards(c *ui.Context) {
	cols := columns(c, 150)
	ui.Grid(c).Columns(cols).GapX(16).GapY(24).Children(func() {
		for i := range a.items {
			// The site gives some lists dramas without an id, so the place in
			// the grid keeps the key apart too.
			a.card(c.Key("d"+a.items[i].ID+":"+strconv.Itoa(i)), &a.items[i])
		}
	})
}

// card is a drama as the lists show it, with how far it's watched on its
// cover when it is.
func (a *app) card(c *ui.Context, it *video.Item) {
	card := ui.ButtonBase(c).Column().Gap(8).AlignItems(ui.Stretch).Justify(ui.Start).FocusRing(false).Label(it.Title)
	hovered := card.Hovered()
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
			if p := a.lib.progressOf(it.ID); p != nil && !p.Done && (p.EpAt > 0 || p.Pos > 0) {
				ui.Box(c).Absolute().Left(6).Bottom(6).Height(18).PaddingX(7).Radius(9).
					Background(ui.RGBA(0, 0, 0, 0.66)).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
					ui.Text(c, cardBadge(p)).FontSize(10.5).TextColor(ui.RGB(255, 255, 255)).SingleLine()
				})
			}
		})
		ui.Text(c, it.Title).FontSize(13).FontWeight(500).MaxLines(2).LineHeight(1.35)
		if it.Subtitle != "" {
			ui.Text(c, it.Subtitle).FontSize(12).TextColor(colors.muted).SingleLine().Margin(-6, 0, 0, 0)
		}
	})
	if card.Clicked() {
		a.openDetail(*it)
	}
}

// cardBadge says on a cover which episode is on, and how far into it.
func cardBadge(p *watched) string {
	s := "第" + strconv.Itoa(p.EpAt+1) + "集"
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
		// The ambience: the cover again, blurred and dimmed, behind the top
		// of the page, fading into the page's own color.
		if bmp := a.covers.get(d.Cover); bmp != nil {
			ui.Box(c).Absolute().Top(0).Left(0).Right(0).Height(300).Clip().Children(func() {
				ui.Image(c, bmp).Absolute().Top(-60).Left(-40).Right(-40).Height(420).
					Fit(ui.Cover).Blur(28).Opacity(0.22)
				grad := ui.Box(c).Absolute().Top(0).Left(0).Right(0).Bottom(0)
				grad.Draw(func(p *ui.Painter, r ui.Rect) {
					p.FillGradient(r, ui.LinearGradient{
						Angle: 180,
						Stops: []ui.GradientStop{
							{Color: ui.RGBA(20, 20, 22, 0.28), At: 0},
							{Color: ui.RGBA(20, 20, 22, 0.55), At: 0.55},
							{Color: colors.bg, At: 1},
						},
					}, 0)
				})
			})
		}
		ui.Column(c).Fill().Padding(10, 24, 32, 24).Children(func() {
			ui.Row(c).Gap(28).AlignItems(ui.Start).Padding(0, 0, 10, 0).Children(func() {
				ui.Box(c).Width(220).Shrink(0).AspectRatio(3.0/4.0).Radius(12).Shadow(0, 8, 24, -6, ui.RGBA(0, 0, 0, 0.45)).Children(func() {
					a.coverFill(c, d.Cover)
				})
				ui.Column(c).Grow(1).Shrink(1).MinWidth(0).Gap(8).Padding(6, 0, 0, 0).Children(func() {
					ui.Text(c, d.Title).FontSize(26).FontWeight(800).LetterSpacing(-0.4).MaxLines(2).LineHeight(1.2)
					meta := d.Subtitle
					if d.Finished {
						meta = strings.TrimSpace(meta + " · 已完结")
					} else if len(d.Episodes) > 0 {
						meta = strings.TrimSpace(meta + " · 连载中")
					}
					if meta != "" {
						ui.Text(c, meta).FontSize(13).TextColor(colors.muted)
					}
					ui.Row(c).Gap(10).AlignItems(ui.Center).Padding(4, 0, 0, 0).Children(func() {
						if d.Score != "" {
							ui.Text(c, "评分 "+d.Score).FontSize(13).TextColor(colors.accent).FontWeight(600)
						}
						heart := iconHeart
						hcolor := colors.muted
						if a.lib.followed(d.ID) {
							heart = iconHeartFill
							hcolor = colors.accent
						}
						fb := ui.ButtonBase(c.Key("follow")).Height(32).PaddingX(14).Radius(999).Center().Gap(7).
							FocusRing(false).Label("追剧").Border(1, colors.hairline).TextColor(hcolor)
						fb.Background(colors.hover)
						fb.Children(func() {
							ui.Icon(c, heart).Size(14, 14)
							ui.Text(c, map[bool]string{true: "已追", false: "追剧"}[a.lib.followed(d.ID)]).FontSize(13).FontWeight(600)
						})
						if fb.Clicked() {
							a.lib.toggleFollow(d.Item)
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
				a.centered(c, a.detailErr, "重试", func() { a.openDetail(d.Item) })
			case len(d.Episodes) == 0:
				a.centered(c, "这部剧还没有可播放的剧集", "", nil)
			default:
				a.episodes(c, d)
			}
		})
	})
}

// detailActions is the row under what a drama is: the play that goes on
// where it stopped, or the first episode.
func (a *app) detailActions(c *ui.Context, d *video.Video) {
	if len(d.Episodes) == 0 {
		return
	}
	p := a.lib.progressOf(d.ID)
	ui.Row(c).Gap(10).AlignItems(ui.Center).Padding(10, 0, 0, 0).Children(func() {
		at, ep := 0, d.Episodes[0]
		label := "播放"
		if p != nil && !p.Done && p.Pos > 10 {
			for i := range d.Episodes {
				if d.Episodes[i].ID == p.EpID {
					at, ep = i, d.Episodes[i]
					break
				}
			}
			label = "继续播放 · 第" + strconv.Itoa(at+1) + "集"
			if p.Dur > 0 {
				label += " · " + clock(time.Duration(p.Pos) * time.Second)
			}
		} else if len(d.Episodes) > 1 {
			label = "播放第1集"
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
	})
}

// episodes is a drama's episodes: a grid of them, or a list when there are
// too many for one page of buttons.
func (a *app) episodes(c *ui.Context, d *video.Video) {
	p := a.lib.progressOf(d.ID)
	ui.Row(c).Margin(14, 0, 10, 0).Children(func() {
		ui.Text(c, "剧集").FontSize(17).FontWeight(700)
		ui.Spacer(c)
		ui.Text(c, itoa(len(d.Episodes))+" 集").FontSize(13).TextColor(colors.muted)
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
func (a *app) episodeRow(c *ui.Context, d *video.Video, ep *video.Episode, at int, p *watched) {
	nowPlaying := a.page == pagePlay && a.playing.item.ID == d.ID && a.playing.ep.ID == ep.ID
	seen := p != nil && (p.Done && p.EpID == ep.ID || p.EpAt > at)
	label := strconv.Itoa(at+1) + "  " + ep.Title
	b := ui.ButtonBase(c.Key("r"+ep.ID+":"+strconv.Itoa(at))).Height(38).PaddingX(12).Radius(8).
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
	p := a.lib.progressOf(d.ID)
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
		stage := ui.ButtonBase(c.Key("stage")).Fill().Center().FocusRing(false).Label("画面")
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
			ui.Text(c, "正在小窗播放").FontSize(13).TextColor(ui.RGBA(255, 255, 255, 0.55))
			if ui.Button(c.Key("back-main"), "回到主窗口").Clicked() {
				a.closePip()
			}
		})
	case a.deviceOutput && a.codie != nil:
		// 画面在设备的屏幕上;这里只说状态。
		ui.Column(c).Gap(12).Center().Children(func() {
			ui.Icon(c, iconFilm).Size(28, 28).TextColor(ui.RGBA(255, 255, 255, 0.35))
			switch {
			case a.playerErr != "":
				ui.Text(c, a.playerErr).TextColor(ui.RGB(255, 255, 255)).FontSize(13).TextAlign(ui.Center).MaxWidth(420)
				if ui.Button(c.Key("retry"), "重试").Clicked() {
					a.playEpisode(playing.item, playing.ep, playing.at)
				}
			case a.codieBusy != "":
				ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
					ui.Spinner(c).TextColor(ui.RGBA(255, 255, 255, 0.7))
					ui.Text(c, a.codieBusy).FontSize(13).TextColor(ui.RGBA(255, 255, 255, 0.7))
				})
			default:
				ui.Text(c, "画面在 codie 设备的屏幕上").FontSize(13).TextColor(ui.RGBA(255, 255, 255, 0.55))
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
				if ui.Button(c.Key("retry"), "重试").Clicked() {
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
				Background(ui.RGBA(0, 0, 0, 0.72)).Border(1, colors.hairline)
			banner.Children(func() {
				if d := a.detail; d != nil && a.playing.at+1 < len(d.Episodes) {
					ui.Text(c, "即将播放 "+d.Episodes[a.playing.at+1].Title).FontSize(13).TextColor(colors.text).SingleLine()
				} else {
					ui.Text(c, "即将播放下一集").FontSize(13).TextColor(colors.text)
				}
				ui.Text(c, strconv.Itoa(a.nextLeft)+" 秒").FontSize(13).TextColor(colors.muted)
				if ui.Button(c.Key("next-now"), "立即播放").Clicked() {
					a.cancelNext()
					a.nextEpisode(false)
				}
				if ui.Button(c.Key("next-cancel"), "取消").Clicked() {
					a.cancelNext()
					a.win.Invalidate()
				}
			})
		})
	}
	if a.drawerOpen {
		a.drawer(c)
	}
	a.controlsOverlay(c, playing)
}

// wizard is the first run's hello: what the app does, in three lines, gone
// for good once taken.
func (a *app) wizard(c *ui.Context) {
	veil := ui.ButtonBase(c.Key("wizard-veil")).Absolute().Top(0).Left(0).Right(0).Bottom(0).
		FocusRing(false).Label("关闭引导")
	veil.Background(ui.RGBA(0, 0, 0, 0.6))
	if veil.Clicked() {
		a.settings.WizardDone = true
		a.settings.save()
	}
	veil.Children(func() {
		card := ui.Column(c).Width(380).Gap(14).Padding(24).Radius(16).
			Background(ui.Hex("#1e1e22")).Border(1, colors.hairline).
			Shadow(0, 24, 60, -12, ui.RGBA(0, 0, 0, 0.6))
		card.Center()
		card.Children(func() {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Icon(c, iconFilm).Size(18, 18).TextColor(colors.accent)
				ui.Text(c, "欢迎使用 Codie 视频").FontSize(17).FontWeight(800)
			})
			for _, line := range []struct {
				icon *ui.SVG
				text string
			}{
				{iconSearch, "左上角切换来源,搜短剧、漫剧和视频,结果按来源分好"},
				{iconClock, "看过的剧在「继续观看」里接着看,喜欢的点「追剧」收着"},
				{iconPip, "点「连接设备」配对 codie,剧就投到它的屏幕上;小窗里边逛边看"},
			} {
				ui.Row(c).Gap(10).AlignItems(ui.Center).Justify(ui.Start).Children(func() {
					ui.Icon(c, line.icon).Size(15, 15).TextColor(colors.accent)
					ui.Text(c, line.text).FontSize(13).TextColor(colors.muted)
				})
			}
			start := ui.ButtonBase(c.Key("wizard-go")).Height(36).PaddingX(20).Radius(999).Center().
				FocusRing(false).Label("开始看剧").Background(colors.accent).TextColor(colors.onAccent)
			start.Children(func() {
				ui.Text(c, "开始看剧").FontSize(13.5).FontWeight(700)
			})
			if start.Clicked() {
				a.settings.WizardDone = true
				a.settings.save()
			}
		})
	})
}

// drawer is the play page's panel of episodes, at the right of the stage.
func (a *app) drawer(c *ui.Context) {
	d := a.detail
	if d == nil || len(d.Episodes) == 0 {
		return
	}
	veil := ui.ButtonBase(c.Key("drawer-veil")).Absolute().Top(0).Left(0).Right(0).Bottom(0).
		FocusRing(false).Label("关闭选集")
	veil.Background(ui.RGBA(0, 0, 0, 0.45))
	if veil.Clicked() {
		a.drawerOpen = false
		a.wakeControls()
	}
	veil.Children(func() {
		panel := ui.Column(c).Width(330).Absolute().Top(0).Right(0).Bottom(0).
			Background(ui.RGBA(16, 16, 18, 0.97)).Border(1, colors.hairline).Children(func() {
			ui.Row(c).Height(48).Shrink(0).Padding(0, 14).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "选集").FontSize(15).FontWeight(700).Grow(1)
				ui.Text(c, itoa(len(d.Episodes))+" 集").FontSize(12).TextColor(colors.muted)
				if b := iconButton(c.Key("drawer-close"), iconX, 13, colors.muted, "关闭选集"); b.Clicked() {
					a.drawerOpen = false
					a.wakeControls()
				}
			})
			ui.Box(c).Height(1).Shrink(0).Background(colors.hairline)
			p := a.lib.progressOf(d.ID)
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
					})
				})
			}
		})
		_ = panel
	})
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

// controlsBody is the controls' content, floating or docked alike.
func (a *app) controlsBody(c *ui.Context, playing playing) {
	// The name of what plays: the drama, then the episode when it says
	// something the drama's name doesn't.
	ui.Row(c).Gap(8).AlignItems(ui.Baseline).Children(func() {
		ui.Text(c, playing.item.Title).FontSize(15).FontWeight(600).Grow(1).Shrink(1).MinWidth(0).SingleLine()
		ep := playing.ep.Title
		if ep != "" && ep != playing.item.Title {
			ui.Text(c, ep).FontSize(12.5).TextColor(colors.muted).Shrink(0).SingleLine()
		}
		if a.nextLeft > 0 {
			ui.Text(c, strconv.Itoa(a.nextLeft)+"秒后连播").FontSize(12).TextColor(colors.accent).Shrink(0)
		}
	})
	if a.deviceOutput && a.codie != nil {
		if a.codieBusy != "" || a.playerErr != "" {
			ui.Text(c, a.codieStatus()).FontSize(12.5).TextColor(colors.muted).SingleLine()
		}
		a.codieScrubber(c)
	} else if player := a.player; player != nil && player.Ready() && player.Err() == nil {
		a.scrubber(c, player)
	} else {
		a.idleBar(c)
	}
	ui.Row(c).Height(38).Gap(6).AlignItems(ui.Center).Children(func() {
		d := a.detail
		if d != nil && a.hasEpisode(-1) {
			if b := iconButton(c.Key("prev"), iconPrev, 15, colors.text, "上一集"); b.Clicked() {
				a.nextEpisode(true)
			}
		}
		if a.deviceOutput && a.codie != nil {
			icon := iconPlay
			if a.codieState != nil && a.codieState.IsPlaying {
				icon = iconPause
			}
			if b := iconButton(c.Key("play"), icon, 17, colors.text, "播放/暂停"); b.Clicked() {
				a.codiePlayPause()
			}
		} else if player := a.player; player != nil {
			icon := iconPlay
			if player.Playing() {
				icon = iconPause
			}
			if b := iconButton(c.Key("play"), icon, 17, colors.text, "播放/暂停"); b.Clicked() {
				a.playPause()
			}
		} else {
			iconButton(c.Key("play"), iconPlay, 17, colors.faint, "播放/暂停")
		}
		if d != nil && a.hasEpisode(1) {
			if b := iconButton(c.Key("next"), iconNext, 15, colors.text, "下一集"); b.Clicked() {
				a.cancelNext()
				a.nextEpisode(false)
			}
		}
		ui.Spacer(c)
		// 选集:一按,剧集的抽屉就来。
		if d != nil && len(d.Episodes) > 0 {
			b := ui.ButtonBase(c.Key("drawer")).Height(30).PaddingX(12).Radius(999).Center().Gap(6).
				FocusRing(false).Label("选集")
			if a.drawerOpen {
				b.Background(colors.hover)
			}
			b.Children(func() {
				ui.Icon(c, iconList).Size(13, 13).TextColor(colors.text)
				ui.Text(c, "选集").FontSize(12.5).FontWeight(600).TextColor(colors.text)
			})
			if b.Clicked() {
				a.drawerOpen = !a.drawerOpen
				a.speedOpen = false
				a.wakeControls()
			}
		}
		// 倍速:一按,快慢的单一选项浮上来。
		if !a.deviceOutput && a.player != nil {
			a.speedControl(c)
		}
		// 小窗:边逛边看。
		if !a.deviceOutput && a.player != nil {
			if b := iconButton(c.Key("pip"), iconPip, 15, colors.text, "小窗播放"); b.Clicked() {
				a.openPip()
			}
		}
		// 全屏:整个屏幕都是画面。
		fs := iconMaximize
		if a.fullscreen {
			fs = iconRestore
		}
		if b := iconButton(c.Key("fs"), fs, 15, colors.text, "全屏"); b.Clicked() {
			a.toggleFullscreen()
		}
		if a.deviceOutput && a.codie != nil {
			ui.Text(c, a.codiePlayedOf()).FontSize(11.5).TextColor(colors.muted)
			a.codieVolumeControl(c)
			a.outputToggle(c)
		} else if player := a.player; player != nil {
			ui.Text(c, a.playedOf(player)).FontSize(11.5).TextColor(colors.muted)
			a.volume(c, player)
		}
	})
}

// speedControl is the button of how fast, with the choices floating over it
// when taken.
func (a *app) speedControl(c *ui.Context) {
	b := ui.ButtonBase(c.Key("speed")).Height(30).PaddingX(10).Radius(999).Center().Gap(5).
		FocusRing(false).Label("倍速")
	if a.speedOpen {
		b.Background(colors.hover)
	}
	b.Children(func() {
		ui.Icon(c, iconSpeed).Size(13, 13).TextColor(colors.text)
		ui.Text(c, rateLabel(a.rate)).FontSize(12.5).FontWeight(600).TextColor(colors.text)
	})
	if b.Clicked() {
		a.speedOpen = !a.speedOpen
		a.wakeControls()
	}
	if a.speedOpen {
		ui.Column(c.Key("speed-pop")).Width(96).Padding(5).Gap(1).Radius(10).
			Absolute().Bottom(58).Right(60).
			Background(ui.Hex("#1e1e22")).Border(1, colors.hairline).
			Shadow(0, 12, 30, -8, ui.RGBA(0, 0, 0, 0.55)).Children(func() {
			for _, r := range []float64{2, 1.5, 1.25, 1, 0.75, 0.5} {
				chosen := a.rate == r
				b := ui.ButtonBase(c.Key("r"+rateLabel(r))).Height(30).PaddingX(12).Radius(7).Center().
					Justify(ui.Center).FocusRing(false).Label(rateLabel(r))
				if chosen {
					b.Background(colors.accent.Alpha(0.22))
				} else if b.Hovered() {
					b.Background(colors.hover)
				}
				b.Children(func() {
					t := ui.Text(c, rateLabel(r)).FontSize(13)
					if chosen {
						t.TextColor(colors.accent).FontWeight(700)
					} else {
						t.TextColor(colors.text)
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
		ui.Box(c).Absolute().Top(8).Left(0).Right(0).Height(4).Radius(2).Background(colors.hairline)
	})
}

// playedOf is how far the episode is and how long it runs, as the controls
// say it, or nothing while there's none.
func (a *app) playedOf(player *ui.VideoPlayer) string {
	if player == nil || !player.Ready() {
		return ""
	}
	return clock(player.Position()) + " / " + clock(player.Duration())
}

// hasEpisode reports whether there's an episode to go to, back or forward
// by dir.
func (a *app) hasEpisode(dir int) bool {
	d := a.detail
	if d == nil {
		return false
	}
	at := a.playing.at + dir
	return at >= 0 && at < len(d.Episodes)
}

// scrubber is how far the episode is, which seeks where it is dragged or
// clicked, once the pointer is off it.
func (a *app) scrubber(c *ui.Context, player *ui.VideoPlayer) {
	st := &a.scrub
	dur := player.Duration().Seconds()
	if !st.active && !st.held {
		st.v = player.Position().Seconds()
	}
	s := ui.SliderBase(c, &st.v, 0, max(dur, 1)).Height(20).FocusRing(false).Label("进度").Cursor(ui.CursorPointer)
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
	if b := iconButton(c.Key("mute"), icon, 14, colors.muted, "静音"); b.Clicked() {
		player.SetMuted(!player.Muted())
	}
	sl := ui.Slider(c.Key("vol"), &a.vol, 0, 100).Width(90)
	if sl.Changed() {
		a.setVolume(a.vol)
		player.SetVolume(a.vol / 100)
	}
}

// outputToggle is 本机 and codie, the two places an episode plays.
func (a *app) outputToggle(c *ui.Context) {
	if a.codie == nil {
		return
	}
	ui.Row(c).Shrink(0).Gap(0).Padding(0, 0, 0, 6).Children(func() {
		for _, o := range []struct {
			label string
			on    bool
		}{{"本机", !a.deviceOutput}, {"codie", a.deviceOutput}} {
			b := ui.ButtonBase(c.Key(o.label)).Height(26).PaddingX(12).Radius(999).Center().FocusRing(false).Label(o.label)
			if o.on {
				b.Background(colors.hover).TextColor(colors.text)
			} else {
				b.Background(ui.Transparent).TextColor(colors.faint)
				if b.Hovered() {
					b.Background(colors.hover)
				}
			}
			b.Children(func() { ui.Text(c, o.label).FontSize(12.5).FontWeight(600) })
			if b.Clicked() {
				a.codieToggle(o.on)
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
	s := ui.SliderBase(c, &st.v, 0, max(secs, 1)).Height(20).FocusRing(false).Label("进度").Cursor(ui.CursorPointer)
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
		a.codieSeek(int64(min(st.v, max(0, secs-1)) * float64(time.Second)))
	case s.Changed():
		a.codieSeek(int64(min(st.v, max(0, secs-1)) * float64(time.Second)))
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

// codieTimes is how far and how long the episode is on the device.
func (a *app) codieTimes() (time.Duration, time.Duration) {
	if st := a.codieState; st != nil {
		return time.Duration(st.PositionMS) * time.Millisecond, time.Duration(st.DurationMS) * time.Millisecond
	}
	return 0, 0
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
	if b := iconButton(c.Key("mute"), icon, 14, colors.muted, "静音"); b.Clicked() {
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
		ui.Text(c, "打开中…").FontSize(13).TextColor(ui.RGBA(255, 255, 255, 0.7))
	})
}

// loading is the while of a page not yet there.
func (a *app) loading(c *ui.Context) {
	ui.Row(c).Fill().Justify(ui.Center).AlignItems(ui.Center).Padding(60, 0, 0, 0).Children(func() {
		ui.Spinner(c).Size(18, 18).Margin(0, 8, 0, 0)
		ui.Text(c, "加载中…").FontSize(13).TextColor(colors.muted)
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
