package app

import (
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"codie-video-player/internal/video"

)

// The window: a dark page of chips over a grid of dramas, a drama's episodes
// under its cover, and the episode playing on a stage of its own. Everything
// is the app's own drawing; the window's buttons sit over the top bar.

// colors are the page's, dark as a cinema's.
var colors = struct {
	bg, text, muted, faint, hairline, hover, press, accent, onAccent ui.Color
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
}

// topBarHeight is the least height of the strip along the top.
const topBarHeight = 40

// view is the content of the window.
func (a *app) view(c *ui.Context) {
	t := *c.Theme()
	t.Dark = true
	c.SetTheme(&t)
	c.Root().Background(colors.bg)

	ui.Column(c).Fill().Children(func() {
		a.topBar(c)
		ui.Box(c).Grow(1).MinHeight(0).Children(func() {
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
}

// topBar is the strip the window buttons sit over: back, the name, and the
// search field. It drags the window where nothing takes the press.
func (a *app) topBar(c *ui.Context) {
	top := float32(12)
	if runtime.GOOS != "darwin" {
		top = 12
	}
	ui.Row(c).Height(topBarHeight).Padding(top, 16, 4, 16).Gap(12).AlignItems(ui.Center).DragWindow().Children(func() {
		if a.page != pageList {
			if b := iconButton(c.Key("back"), iconBack, 16, colors.text, "返回"); b.Clicked() {
				a.back()
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

// searchField is what a search is typed into, which searches where Enter is
// pressed, and clears by its cross.
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
			PaddingStart(29).PaddingEnd(end).Placeholder("搜短剧、漫剧").FontSize(13).Label("搜索")
		if in.Submitted() {
			a.search(a.query)
		}
		ui.Icon(c, iconSearch).Size(13, 13).TextColor(colors.muted).Absolute().Left(9).Top(7.5).IgnoreInput(true)
		if clearable {
			x := ui.ButtonBase(c.Key("clear")).Size(22, 22).Center().Absolute().Right(4).Top(4).
				KeepFocus().FocusRing(false).Label("清除搜索")
			if x.Clicked() {
				a.query = ""
				if a.searching {
					a.searching = false
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

// listView is the chips over the grid of what the tab lists. A source that
// makes no chips of its own, as one that only searches, says so instead.
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
			a.grid(c)
		})
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

// chips are the kinds of drama the source lists, one to each, the one
// showing raised. A source that makes none gets a line saying what it does
// instead.
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
		a.centered(c, a.gridErr, "重试", func() { a.loadTab() })
	case a.gridLoading && len(a.items) == 0:
		a.loading(c)
	case len(a.items) == 0:
		if a.searching {
			a.centered(c, "没找到", "", nil)
		} else {
			a.centered(c, "这里还没有内容", "", nil)
		}
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

// card is a drama as the lists show it.
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

// detailView is a drama's page: its cover, what it is, and its episodes one
// button to each.
func (a *app) detailView(c *ui.Context) {
	d := a.detail
	if d == nil {
		return
	}
	ui.Scroll(c.Key("detail")).Fill().Padding(10, 24, 32, 24).Children(func() {
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
				if d.Score != "" {
					ui.Text(c, "评分 "+d.Score).FontSize(13).TextColor(colors.accent).FontWeight(600)
				}
				if d.Intro != "" {
					ui.Text(c, d.Intro).FontSize(13).TextColor(colors.muted).MaxLines(6).LineHeight(1.5).Margin(4, 0, 0, 0)
				}
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
			ui.Row(c).Margin(14, 0, 10, 0).Children(func() {
				ui.Text(c, "剧集").FontSize(17).FontWeight(700)
				ui.Spacer(c)
				ui.Text(c, itoa(len(d.Episodes))+" 集").FontSize(13).TextColor(colors.muted)
			})
			ui.Grid(c).Columns(columns(c, 120)).GapX(10).GapY(10).Children(func() {
				for i := range d.Episodes {
					a.episode(c.Key(d.Episodes[i].ID+":"+strconv.Itoa(i)), d, &d.Episodes[i], i)
				}
			})
		}
	})
}

// episode is one episode as a button, the one playing marked.
func (a *app) episode(c *ui.Context, d *video.Video, ep *video.Episode, at int) {
	nowPlaying := a.page == pagePlay && a.playing.item.ID == d.ID && a.playing.ep.ID == ep.ID
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
		}
		if ep.Seconds > 0 {
			ui.Text(c, clock(time.Duration(ep.Seconds)*time.Second)).FontSize(12).TextColor(colors.faint)
		}
	})
	if b.Clicked() {
		a.playEpisode(d.Item, *ep, at)
	}
}

// playView is the episode playing: it on the stage, under it what plays,
// pauses, moves about it, and how loud it is.
func (a *app) playView(c *ui.Context) {
	playing := a.playing
	ui.Column(c).Fill().Children(func() {
		ui.Box(c).Grow(1).MinHeight(0).Background(ui.RGB(0, 0, 0)).Center().Children(func() {
			if a.deviceOutput && a.codie != nil {
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
			} else {
				player := a.player
				switch {
				case player != nil:
					ui.Video(c, player).Fill().Fit(ui.Contain)
					if !player.Ready() {
						a.opening(c)
					}
				case a.playerErr != "":
					ui.Column(c).Gap(12).Center().Children(func() {
						ui.Text(c, a.playerErr).TextColor(ui.RGB(255, 255, 255)).FontSize(13).TextAlign(ui.Center).MaxWidth(420)
						if ui.Button(c.Key("retry"), "重试").Clicked() {
							a.playEpisode(playing.item, playing.ep, playing.at)
						}
					})
				default:
					a.opening(c)
				}
			}
		})
		a.controls(c, playing)
	})
}

// opening is the while of an episode on its way to the stage.
func (a *app) opening(c *ui.Context) {
	ui.Row(c).Gap(10).Center().Children(func() {
		ui.Spinner(c).TextColor(ui.RGBA(255, 255, 255, 0.7))
		ui.Text(c, "打开中…").FontSize(13).TextColor(ui.RGBA(255, 255, 255, 0.7))
	})
}

// controls is what an episode answers: its name, how far it is, and the
// buttons of playing it.
func (a *app) controls(c *ui.Context, playing playing) {
	ui.Column(c).Shrink(0).Padding(10, 16, 14, 16).Gap(8).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Baseline).Children(func() {
			ui.Text(c, playing.item.Title).FontSize(15).FontWeight(600).Grow(1).Shrink(1).MinWidth(0).SingleLine()
			if playing.ep.Title != "" {
				ui.Text(c, playing.ep.Title).FontSize(12.5).TextColor(colors.muted).Shrink(0).SingleLine()
			}
		})
		if a.deviceOutput && a.codie != nil {
			a.codieScrubber(c)
		} else if player := a.player; player != nil && player.Ready() && player.Err() == nil {
			a.scrubber(c, player)
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
					if player.Playing() {
						player.Pause()
					} else {
						player.Play()
					}
				}
			} else {
				iconButton(c.Key("play"), iconPlay, 17, colors.faint, "播放/暂停")
			}
			if d != nil && a.hasEpisode(1) {
				if b := iconButton(c.Key("next"), iconNext, 15, colors.text, "下一集"); b.Clicked() {
					a.nextEpisode(false)
				}
			}
			ui.Spacer(c)
			if a.deviceOutput && a.codie != nil {
				ui.Text(c, a.codiePlayedOf()).FontSize(11.5).TextColor(colors.muted)
				a.codieVolumeControl(c)
			} else if player := a.player; player != nil {
				ui.Text(c, a.playedOf(player)).FontSize(11.5).TextColor(colors.muted)
				a.volume(c, player)
			}
		})
		a.outputToggle(c)
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
		case ui.InputPointerUp:
			st.held = false
		}
		return false
	})
	seek := func() {
		player.Seek(time.Duration(min(st.v, max(0, dur-1)) * float64(time.Second)))
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
		p.Fill(ui.Rect{X: r.X, Y: y, W: r.W, H: thick}, colors.hairline, thick/2)
		p.Fill(ui.Rect{X: r.X, Y: y, W: max(thick, r.W*frac), H: thick}, colors.accent, thick/2)
	})
}

// volume is how loud the episode is, its icon saying whether it's muted. The
// app keeps the level from episode to episode.
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
		player.SetVolume(a.vol / 100)
	}
}

// outputToggle is 本机 and codie, the two places an episode plays.
func (a *app) outputToggle(c *ui.Context) {
	if a.codie == nil {
		return
	}
	ui.Row(c).Shrink(0).Gap(0).Padding(0, 0, 2, 0).Children(func() {
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
		p.Fill(ui.Rect{X: r.X, Y: y, W: r.W, H: thick}, colors.hairline, thick/2)
		p.Fill(ui.Rect{X: r.X, Y: y, W: max(thick, r.W*frac), H: thick}, colors.accent, thick/2)
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
		a.codieVolume(a.vol / 100)
	}
	sl := ui.Slider(c.Key("vol"), &a.vol, 0, 100).Width(90)
	if sl.Changed() {
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
