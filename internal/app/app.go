// Package app is the whole of the player: its state, and what its window
// shows. Everything in the state is read and changed on the main thread; what
// a load or a cover brings comes back through post.
package app

import (
	"embed"
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"codie-video-player/internal/codieout"
	"codie-video-player/internal/i18n"
	"codie-video-player/internal/plugin"
	"codie-video-player/internal/source/huangguo"
	"codie-video-player/internal/video"
)

// app is what the window shows and does.
type app struct {
	win *mygo.Window

	// The sources of videos: the built-in Huangguo first, then the plugins'.
	// The one showing is embedded: what the grid holds is of it.
	sources []sourceEntry
	*sourceEntry
	plugins []*plugin.Plugin
	bundled embed.FS

	// A page of the window.
	page pageKind

	// The list page: which tab shows, and what the grid holds of it.
	tabID       string
	tabs        []video.Tab
	tabsLoading bool
	query       string // what's in the search field
	searchBox   ui.Handle
	searching   bool // the grid shows what a search found
	items       []video.Item
	gridPage    int // the last page loaded, 0 for none
	gridLoading bool
	gridErr     string

	// The detail page.
	detail        *video.Video
	detailOf      string // the drama the page is of
	detailLoading bool
	detailErr     string

	// The player page.
	playing    playing
	player     *ui.VideoPlayer
	playerFile string // a file ffmpeg made, which goes when another comes
	playerErr  string
	scrub      scrubState
	vol        float64

	// 在 codie 上播放:转好的 MP4 开成设备的媒体会话,画面落在它的屏幕上。
	deviceOutput bool
	codieSession string
	codieFile    string
	codieState   *codieout.MediaSessionState
	codieBusy    string

	covers *coverCache

	settings *settings

	// The codie device, paired, and where its dialog is.
	codie       *codieout.Client
	deviceWin   *mygo.Window
	pair        pairState
	pairAddrBox ui.Handle
	pairCodeBox ui.Handle
	probe       []codieout.ProbeResult
	mediaProbe  []codieout.MediaSessionProbe
	probing     bool
	deviceNote  string
}

// pageKind is one of the window's pages.
type pageKind int

const (
	pageList pageKind = iota
	pageDetail
	pagePlay
)

// playing says what the player is of, and where its episode sits.
type playing struct {
	item video.Item
	ep   video.Episode
	at   int
}

// post runs fn on the main thread and redraws.
func (a *app) post(fn func()) {
	if a.win == nil {
		return
	}
	a.win.Update(func() {
		fn()
		a.win.Invalidate()
	})
}

// Run runs the app until it quits.
func Run(bundledPlugins embed.FS) error {
	if !mygo.App.RequestSingleInstanceLock() {
		return nil
	}
	a := &app{vol: 100, bundled: bundledPlugins}
	a.covers = newCovers(a.post)
	mygo.App.WhenReady(func() {
		a.settings = loadSettings()
		a.makeSources(bundledPlugins)
		a.loadCodie()
		i18nReady()
		a.win = mygo.NewWindow(mygo.WindowOptions{
			Title:     "Codie 视频",
			Width:     1180,
			Height:    760,
			MinWidth:  920,
			MinHeight: 600,
			StateKey:  "main",
			// The chrome is the app's own: a dark bar of chips over the grid.
			TitleBarStyle:   mygo.TitleBarHidden,
			BackgroundColor: "#141416",
			Content:         ui.View(a.view),
		})
		a.loadTabs()
		a.loadTab()
		if shotHook != nil {
			shotHook(a)
		}
		// Closing the window hides it: what plays goes on, and the Dock's
		// or another launch's brings it back.
		a.win.OnClose(func(e *mygo.CloseEvent) {
			log.Printf("lifecycle: window close event")
			e.PreventDefault()
			a.win.Hide()
		})
		mygo.App.OnBeforeQuit(func(*mygo.QuitEvent) {
			log.Printf("lifecycle: before-quit")
		})
	})
	mygo.App.OnActivate(func(bool) {
		if a.win != nil {
			a.win.Show()
			a.win.Focus()
		}
	})
	mygo.App.OnSecondInstance(func([]string, string) {
		if a.win != nil {
			a.win.Show()
			a.win.Focus()
		}
	})
	return mygo.App.Run()
}

// ---- the sources ----

// makeSources lists where videos come from: the built-in Huangguo first, then
// the plugins that play videos, the one last used showing.
func (a *app) makeSources(bundledPlugins embed.FS) {
	plugin.SetOpenURL(mygo.Shell.OpenExternal)
	plugin.SetAppVersion(mygo.App.Version())
	plugin.SetWebSignIn(func(plugin.WebSignIn) (string, error) {
		return "", errors.New("这个应用不登录账号")
	})
	ensureBundledPlugins(bundledPlugins)
	a.plugins = plugin.Discover(pluginsDir(), userDataDir("plugin-data"))
	a.sources = []sourceEntry{{id: "huangguo", name: "黄果短剧", src: &huangguo.Source{}}}
	for _, p := range a.plugins {
		if v := p.Video(); v != nil {
			e := sourceEntry{id: v.Info().ID, name: v.Info().Name, src: v}
			if len(p.Permissions().Process) > 0 && !p.Granted() {
				e.grant = p
			}
			a.sources = append(a.sources, e)
		}
	}
	if a.sourceEntry == nil || a.sourceOf(a.sourceEntry.id) == nil {
		a.sourceEntry = a.sourceOf(a.settings.Source)
		if a.sourceEntry == nil {
			a.sourceEntry = &a.sources[0]
		}
	}
}

// sourceOf finds the source of id, or nil.
func (a *app) sourceOf(id string) *sourceEntry {
	for i := range a.sources {
		if a.sources[i].id == id {
			return &a.sources[i]
		}
	}
	return nil
}

// useSource makes the source of id the one showing, back at its first tab.
func (a *app) useSource(id string) {
	e := a.sourceOf(id)
	if e == nil || a.sourceEntry == e {
		return
	}
	a.stopPlayer()
	a.sourceEntry = e
	a.settings.Source = id
	a.settings.save()
	a.page = pageList
	a.detail = nil
	a.searching = false
	a.query = ""
	a.tabID = ""
	a.tabs, a.tabsLoading = nil, false
	a.clearGrid()
	a.loadTabs()
	a.loadTab()
}

// grantSource lets the plugin of id start the programs it names, which its
// streams play through.
func (a *app) grantSource(id string) {
	for _, p := range a.plugins {
		if p.Info().ID == id {
			p.Grant(true)
		}
	}
	a.makeSources(a.bundled)
	if e := a.sourceOf(a.sourceEntry.id); e != nil {
		a.sourceEntry = e
	}
	a.loadTab()
}

// ---- the tabs and the grid ----

// loadTabs asks the source what its chips are.
func (a *app) loadTabs() {
	src := a.sourceEntry
	if src == nil || !src.src.Can("videoTabs") {
		a.tabs, a.tabsLoading = nil, false
		a.tabID = ""
		return
	}
	if a.tabsLoading {
		return
	}
	a.tabsLoading = true
	go func() {
		tabs, err := src.src.Tabs()
		a.post(func() {
			a.tabsLoading = false
			if err != nil {
				return
			}
			a.tabs = tabs
			if len(tabs) > 0 && !a.hasTab(a.tabID) {
				a.tabID = tabs[0].ID
				a.loadTab()
			}
		})
	}()
}

func (a *app) hasTab(id string) bool {
	for _, t := range a.tabs {
		if t.ID == id {
			return true
		}
	}
	return false
}

// useTab makes the tab of id the one showing, and loads what it lists.
func (a *app) useTab(id string) {
	if a.tabID == id && !a.searching {
		return
	}
	a.tabID = id
	a.searching = false
	a.clearGrid()
	a.loadTab()
}

// clearGrid forgets what the grid holds.
func (a *app) clearGrid() {
	a.items, a.gridPage = nil, 0
	a.gridLoading, a.gridErr = false, ""
}

// loadTab loads the first of what the tab lists, asking anew.
func (a *app) loadTab() {
	a.clearGrid()
	a.loadMore()
}

// loadMore loads the next page of what the tab lists into the grid, by the
// source's own counting: pages from one.
func (a *app) loadMore() {
	src := a.sourceEntry
	if src == nil || a.gridLoading || a.searching {
		return
	}
	// The chips are on their way: the first load goes out under their first
	// name, not under the last source's tab or nothing at all.
	if a.tabID == "" && src.src.Can("videoTabs") {
		return
	}
	next := a.gridPage + 1
	a.gridLoading = true
	go func() {
		l, err := src.src.List(a.tabID, next)
		a.post(func() {
			a.gridLoading = false
			if err != nil {
				a.gridErr = videoProblem(err, "这一页没加载出来")
				return
			}
			a.gridPage = next
			a.items = append(a.items, l.Items...)
		})
	}()
}

// search asks the source what q is, and the grid shows it.
func (a *app) search(q string) {
	q = strings.TrimSpace(q)
	src := a.sourceEntry
	if q == "" || src == nil {
		return
	}
	a.searching = true
	a.clearGrid()
	a.gridLoading = true
	go func() {
		r, err := src.src.Search(q, 0)
		a.post(func() {
			a.gridLoading = false
			if err != nil {
				a.gridErr = videoProblem(err, "搜索失败")
				return
			}
			a.items = r.Items
		})
	}()
}

// openDetail opens a drama's page, asking for it anew.
func (a *app) openDetail(item video.Item) {
	src := a.sourceOf(item.Source)
	if src == nil {
		src = a.sourceEntry
	}
	if src == nil {
		return
	}
	a.sourceEntry = src
	a.page = pageDetail
	a.detailOf = item.ID
	a.detail = &video.Video{Item: item}
	a.detailLoading = true
	a.detailErr = ""
	go func() {
		v, err := src.src.Video(item.ID)
		a.post(func() {
			if a.page != pageDetail || a.detailOf != item.ID {
				return
			}
			a.detailLoading = false
			if err != nil {
				a.detailErr = videoProblem(err, "这部剧没加载出来")
				return
			}
			a.detail = v
		})
	}()
}

// back goes back a page of the window.
func (a *app) back() {
	switch a.page {
	case pagePlay:
		a.stopPlayer()
		a.playerErr = ""
		a.playing = playing{}
		a.page = pageDetail
	case pageDetail:
		a.page = pageList
		a.detail = nil
	}
}

// playEpisode opens an episode in the player, of the source it came from.
func (a *app) playEpisode(item video.Item, ep video.Episode, at int) {
	src := a.sourceOf(item.Source)
	if src == nil {
		src = a.sourceEntry
	}
	if src == nil {
		return
	}
	// 在 codie 上播放:取流后交给设备的媒体会话。
	if a.deviceOutput && a.codie != nil {
		go func() {
			log.Printf("codie play: resolving %s / %s", item.ID, ep.ID)
			s, err := src.src.Play(item.ID, ep.ID)
			if err != nil {
				log.Printf("codie play: resolve failed: %v", err)
				a.post(func() {
					a.page = pagePlay
					a.playing = playing{item: item, ep: ep, at: at}
					a.playerErr = videoProblem(err, "这一集放不了")
				})
				return
			}
			log.Printf("codie play: resolved (%s, audio=%v)", s.Type, s.AudioURL != "")
			a.post(func() { a.playEpisodeOnDevice(item, ep, at, s) })
		}()
		return
	}
	a.stopPlayer()
	a.playerErr = ""
	a.playing = playing{item: item, ep: ep, at: at}
	a.page = pagePlay
	go func() {
		s, err := src.src.Play(item.ID, ep.ID)
		if err != nil {
			a.post(func() { a.playerErr = videoProblem(err, "这一集放不了") })
			return
		}
		p, file, err := openEpisodeStream(s)
		a.post(func() {
			if a.playing.item.ID != item.ID || a.playing.ep.ID != ep.ID || a.page != pagePlay {
				if file != "" {
					go os.Remove(file)
				}
				return
			}
			if err != nil {
				a.playerErr = err.Error()
				return
			}
			a.player, a.playerFile = p, file
			p.SetVolume(a.vol / 100)
		})
	}()
}

// nextEpisode opens the episode after the one playing, or the one before.
func (a *app) nextEpisode(back bool) {
	d := a.detail
	if d == nil {
		return
	}
	at := a.playing.at + 1
	if back {
		at = a.playing.at - 1
	}
	if at < 0 || at >= len(d.Episodes) {
		return
	}
	a.playEpisode(d.Item, d.Episodes[at], at)
}

// stopPlayer ends what plays, and its file — here or on the device.
func (a *app) stopPlayer() {
	a.stopPlayerForCodie()
	p, file := a.player, a.playerFile
	a.player, a.playerFile = nil, ""
	if p != nil {
		p.Close()
	}
	if file != "" {
		go os.Remove(file)
	}
}

// ---- the small things ----

// videoProblem makes out what went wrong with what a source was asked, for a
// page's error state: msg, with what the source said of it.
func videoProblem(err error, msg string) string {
	switch {
	case errors.Is(err, video.ErrSignIn), errors.Is(err, video.ErrSetup),
		errors.Is(err, video.ErrUnavailable), errors.Is(err, video.ErrUnsupported):
		if reason := video.Reason(err); reason != "" {
			return reason
		}
		return msg
	}
	return msg + ": " + err.Error()
}

// settings is what the app keeps of the listener's.
type settings struct {
	Source string `json:"source,omitempty"`
	path   string
}

func (s *settings) save() {
	if s.path == "" {
		return
	}
	b, _ := json.MarshalIndent(s, "", "  ")
	os.WriteFile(s.path, b, 0o600)
}

func loadSettings() *settings {
	s := &settings{}
	dir, err := mygo.App.Path(mygo.PathUserData)
	if err == nil {
		s.path = filepath.Join(dir, "settings.json")
		if b, err := os.ReadFile(s.path); err == nil {
			json.Unmarshal(b, s)
		}
	}
	return s
}

// pluginsDir is where plugins are dropped in.
func pluginsDir() string { return userDataDir("plugins") }

func userDataDir(sub string) string {
	dir, err := mygo.App.Path(mygo.PathUserData)
	if err != nil {
		dir = filepath.Join(os.TempDir(), "codie-video-player")
	}
	return filepath.Join(dir, sub)
}

// i18nReady speaks what the system speaks, for the plugins' translations.
func i18nReady() {
	lang := i18n.English
	for _, l := range mygo.App.PreferredLanguages() {
		if strings.HasPrefix(l, "zh") {
			lang = i18n.Chinese
			break
		}
		if strings.HasPrefix(l, "en") {
			lang = i18n.English
			break
		}
	}
	i18n.SetLanguage(lang)
}
