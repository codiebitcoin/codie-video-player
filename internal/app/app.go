// Package app is the whole of the player: its state, and what its window
// shows. Everything in the state is read and changed on the main thread; what
// a load or a cover brings comes back through post.
package app

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"log"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"codie-video-player/internal/codieout"
	"codie-video-player/internal/i18n"
	"codie-video-player/internal/library"
	"codie-video-player/internal/plugin"
	"codie-video-player/internal/proxy"
	"codie-video-player/internal/source/local"
	"codie-video-player/internal/video"
)

// app is what the window shows and does.
type app struct {
	win *mygo.Window

	// The sources of videos: the local folders first, then the plugins. The
	// one showing is embedded: what the grid holds is of it. localSrc is the
	// local one, whose folders the settings keep.
	sources []sourceEntry
	*sourceEntry
	localSrc *local.Source
	plugins  []*plugin.Plugin
	bundled  embed.FS

	// A page of the window.
	page pageKind

	// The list page: which tab shows, and what the grid holds of it.
	tabID         string
	tabs          []video.Tab
	tabsLoading   bool
	tabsErr       string // the source gave no chips at all, and why
	query         string // what's in the search field
	searchBox     ui.Handle
	searching     bool // the grid shows what a search found
	items         []video.Item
	gridPage      int  // the last page loaded, 0 for none
	gridMore      bool // there is a next page to load
	gridLoading   bool
	gridErr       string
	retried       bool // a cold source's first ask already asked again
	playlistQuery string
	playlistBox   ui.Handle

	// A playlist playing in sequence: the videos in order (or shuffled),
	// which one is on now, and the playlist's name for the drawer.
	queue      []video.Item
	queueAt    int
	queueTitle string
	queueList  ui.ListState

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

	// How the player behaves beyond the basics: how fast it plays, whether
	// the window is fullscreen, the episode drawer and the rate popover,
	// the seconds to the next episode, and the little window.
	rate       float64
	fullscreen bool
	drawerOpen bool
	speedOpen  bool
	nextLeft   int         // seconds to the next episode, 0 for none
	nextTimer  *time.Timer // counting it down
	nextLatch  bool        // this episode already counted down once
	resumeAt   float64     // seconds into the episode it opens at
	watchTimer *time.Timer // records how far the episode is, now and then
	pipWin     *mygo.Window
	epsList    ui.ListState // the drawer's long list of episodes

	// The viewer's own: follows, history, and how far each drama is.
	lib *library.Library

	// Where a back can go forward to.
	fwd []location

	// A source's sign-in: the dialog that asks for one, the account the
	// plugin knows, and the sign-in pages it has open, by plugin id.
	askSignIn   bool
	signInBusy  bool
	signInNote  string
	srcAcct     *srcAccount
	favOpen     bool // the sidebar's 我的收藏 group taken in or not
	signInPages map[string]*mygo.Window

	// 在 codie 上播放:选角色槽位后再放。slotOpen 是选择框,slotChoice 是选中的
	// 槽位,deviceSlot 是设备现在站着的那个,slotPlay 是确认后要放的这一集。
	slotOpen   bool
	slotChoice int
	deviceSlot int
	slotBusy   bool
	slotErr    string
	slotPlay   playing

	// macOS's own no to a program a plugin started, when it happens: the
	// dialog says so, and its retry asks the system again, which lets it
	// through. macRetry is the ask to run again.
	macRetry func()

	// The controls over the picture: whether they show, and when to take
	// them away again.
	controlsOn  bool
	controlsAt  time.Time
	controlsTmr *time.Timer

	// A search's answers, one group to a source, and which of them shows.
	searchGroups []searchGroup
	searchFilter string // "all", or one source's id

	// An update, when the repo has one newer than the app.
	updateTag      string
	updateURL      string
	updateChecking bool

	saveTmr  *time.Timer // the settings' write, once the changes settle
	quitting bool        // a real quit is on: the window closes instead of hiding

	// 在 codie 上播放:转好的 MP4 开成设备的媒体会话,画面落在它的屏幕上。
	// 超出 128MiB 的长视频会拆成多段 codieSegs 顺序无缝接力,对外仍是一集。
	deviceOutput      bool
	codieSession      string
	codieFile         string
	codieSegs         []codieSegment
	codieSegAt        int
	codieTotalMS      int64
	codieSwitchingSeg bool
	// codieMore says later segments are still being made: the last one on the
	// screen mustn't count the next episode in while they are.
	codieMore  bool
	codieGen   uint64
	codieState *codieout.MediaSessionState
	codieBusy  string
	// codieCancel ends the making of the episode's segments when the playback
	// they were for is over: a stopped or replaced episode leaves no ffmpeg
	// running behind it.
	codieCancel func()
	// 预转码:播放列表里下一部的第一集已经为设备转好,轮到它时不必再等。
	prepared  *codiePrepared
	precoding bool

	// The conversation tools: the catalog served to the paired device, so
	// what the viewer says to it ("next one", "play …") moves the playback.
	tools *agentTools

	covers *coverCache

	settings *settings

	// The codie device, paired, and where its dialog is.
	codie       *codieout.Client
	deviceWin   *mygo.Window
	settingsWin *mygo.Window
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

// current is the one app there is, for the tray's menu and such, which the
// Run closure holds otherwise.
var current *app

// Run runs the app until it quits.
func Run(bundledPlugins embed.FS) error {
	if !mygo.App.RequestSingleInstanceLock() {
		return nil
	}
	setupLog()
	a := &app{vol: 100, favOpen: true, bundled: bundledPlugins}
	current = a
	a.covers = newCovers(a.post)
	mygo.App.WhenReady(func() {
		a.settings = loadSettings()
		a.lib = library.Load(userDataDir(""))
		proxy.Set(a.settings.Proxy)
		cleanCodiePrepared()
		if a.settings.Appearance == "" {
			a.settings.Appearance = AppearanceSystem
		}
		if a.settings.Language == "" {
			// English unless the listener picks another; the system's own
			// language is a choice too, not the default.
			a.settings.Language = i18n.English
		}
		a.setLanguage()
		a.applyPalette()
		a.vol = a.settings.Volume
		if a.vol <= 0 {
			a.vol = 100
		}
		a.rate = a.settings.Rate
		if a.rate <= 0 {
			a.rate = 1
		}
		a.makeSources(bundledPlugins)
		a.loadCodie()
		if a.codie != nil {
			// 连着设备就默认投它:点开一集直接落在大屏上,不用每回先切。
			a.deviceOutput = true
		}
		log.Printf("app ready: version=%s lang=%s codie=%v", mygo.App.Version(), a.settings.Language, a.codie != nil)
		a.checkForUpdates()
		makeTray()
		a.win = mygo.NewWindow(mygo.WindowOptions{
			Title:     i18n.T("Codie Video"),
			Width:     1180,
			Height:    760,
			MinWidth:  920,
			MinHeight: 600,
			StateKey:  "main",
			// The strip along the top is the app's own; the traffic lights sit
			// centered in it, and the strip's first element leaves them room.
			TitleBarHeight:       topBarHeight,
			TrafficLightPosition: &mygo.Point{X: 20, Y: 14},
			// The chrome is the app's own: a dark bar of chips over the grid.
			TitleBarStyle:   mygo.TitleBarHidden,
			BackgroundColor: "#141416",
			Content:         ui.View(a.view),
		})
		a.loadTabs()
		a.loadTab()
		a.loadAccount(true)
		if shotHook != nil {
			shotHook(a)
		}
		// Closing the window hides it: what plays goes on, and the Dock's
		// or another launch's brings it back. A real quit — the tray's, the
		// Dock's — lets the window close, which the quit needs to finish.
		a.win.OnClose(func(e *mygo.CloseEvent) {
			log.Printf("lifecycle: window close event")
			if a.quitting {
				return
			}
			e.PreventDefault()
			a.win.Hide()
		})
		mygo.App.OnBeforeQuit(func(*mygo.QuitEvent) {
			log.Printf("lifecycle: before-quit")
			a.quitting = true
			a.recordPlayProgress()
			if a.tools != nil {
				a.tools.stop()
			}
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

// makeSources lists where videos come from: the local folders first, then the
// plugins that play videos, the one last used showing. A plugin the listener
// turned off is still loaded, so the settings can turn it back on, but it
// isn't a source until then.
func (a *app) makeSources(bundledPlugins embed.FS) {
	plugin.SetOpenURL(mygo.Shell.OpenExternal)
	plugin.SetAppVersion(mygo.App.Version())
	plugin.SetWebSignIn(a.webSignIn)
	ensureBundledPlugins(bundledPlugins)
	a.plugins = plugin.Discover(pluginsDir(), userDataDir("plugin-data"))
	a.localSrc = &local.Source{Folders: append([]string(nil), a.settings.LocalFolders...)}
	a.sources = []sourceEntry{
		{id: local.ID, name: i18n.T("Local"), src: a.localSrc},
	}
	for _, p := range a.plugins {
		// The listener's yes is the default: a plugin's programs — ffmpeg,
		// yt-dlp — run without a banner asking again. macOS still has its
		// own say, which a failed run's dialog asks past.
		if len(p.Permissions().Process) > 0 && !p.Granted() {
			if err := p.Grant(true); err != nil {
				log.Printf("grant %s: %v", p.Info().ID, err)
			} else {
				log.Printf("granted process permission to %s", p.Info().ID)
			}
		}
		if a.pluginDisabled(p.Info().ID) {
			continue
		}
		if v := p.Video(); v != nil {
			e := sourceEntry{id: v.Info().ID, name: v.Info().Name, src: v, plugin: p}
			a.sources = append(a.sources, e)
			a.tellPluginChoices(p)
		}
	}
	// The dropdown reads: the local folders, then YouTube, then Bilibili,
	// then NicoNico, then the other plugins, Huangguo last.
	slices.SortStableFunc(a.sources, func(x, y sourceEntry) int {
		rank := func(e sourceEntry) int {
			switch e.id {
			case local.ID:
				return 0
			case "youtube":
				return 1
			case "bilibili":
				return 2
			case "niconico":
				return 3
			case "huangguo":
				return 5
			default:
				return 4
			}
		}
		return rank(x) - rank(y)
	})
	if a.sourceEntry == nil || a.sourceOf(a.sourceEntry.id) == nil {
		a.sourceEntry = a.sourceOf(a.settings.Source)
		if a.sourceEntry == nil {
			a.sourceEntry = &a.sources[0]
		}
	}
}

// tellPluginChoices hands a plugin what the listener chose that the source
// needs to know: how tall a picture to take, and whether subtitles play.
func (a *app) tellPluginChoices(p *plugin.Plugin) {
	if a.settings == nil {
		return
	}
	p.SetSetting("maxHeight", strconv.Itoa(a.settings.MaxHeight))
	subtitles := "0"
	if a.settings.Subtitles {
		subtitles = "1"
	}
	p.SetSetting("subtitles", subtitles)
}

// sourceDisabled reports whether a built-in source is turned off.
func (a *app) sourceDisabled(id string) bool {
	for _, d := range a.settings.DisabledSources {
		if d == id {
			return true
		}
	}
	return false
}

// setSourceDisabled turns a built-in source off or on, and the list of
// sources with it.
func (a *app) setSourceDisabled(id string, disabled bool) {
	on := a.sourceDisabled(id)
	if disabled == on {
		return
	}
	if disabled {
		a.settings.DisabledSources = append(a.settings.DisabledSources, id)
		if a.sourceEntry != nil && a.sourceEntry.id == id {
			a.settings.Source = ""
			a.sourceEntry = nil
		}
	} else {
		for i, d := range a.settings.DisabledSources {
			if d == id {
				a.settings.DisabledSources = append(a.settings.DisabledSources[:i], a.settings.DisabledSources[i+1:]...)
				break
			}
		}
	}
	a.settings.save()
	a.makeSources(a.bundled)
	a.loadTabs()
	a.loadTab()
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
	a.detailOf = ""
	a.searching = false
	a.searchGroups = nil
	a.query = ""
	a.playlistQuery = ""
	a.clearQueue()
	a.tabID = ""
	a.tabs, a.tabsLoading = nil, false
	a.fwd = nil
	a.clearGrid()
	a.loadTabs()
	a.loadTab()
	a.loadAccount(true)
}

// srcAccount is what a source's plugin says of the listener there.
type srcAccount struct {
	ID    string
	Name  string
	Image string
}

// loadAccount asks the showing source's plugin who the listener is there, for
// the sidebar's account row. ask says whether a source freshly showing should
// prompt for a sign-in when it has none: the listener asked for that.
func (a *app) loadAccount(ask bool) {
	p := a.sourceEntry.plugin
	a.srcAcct = nil
	if p == nil || !p.Can("account") {
		a.askSignIn = false
		return
	}
	go func() {
		var me struct {
			ID    string `json:"id"`
			Name  string `json:"name"`
			Image string `json:"imageUrl"`
		}
		err := p.Call("account", nil, &me)
		if err != nil {
			log.Printf("account %s: %v", p.Info().ID, err)
		}
		a.post(func() {
			if a.sourceEntry == nil || a.sourceEntry.plugin != p {
				return // another source is showing by now
			}
			// Signed out, or the ask failed: nothing to show. A source asks
			// for its sign-in once, once the first run's hello is past; the
			// sidebar's own button stays the way in after that.
			if err != nil || me.ID == "" {
				a.srcAcct = nil
				if ask && a.settings.WizardDone && !a.signInAsked(p.Info().ID) {
					a.askSignIn = true
					a.markSignInAsked(p.Info().ID)
				}
				return
			}
			a.srcAcct = &srcAccount{ID: me.ID, Name: me.Name, Image: me.Image}
			a.askSignIn = false
		})
	}()
}

// signInAsked reports whether a source's sign-in was already asked for.
func (a *app) signInAsked(id string) bool {
	for _, d := range a.settings.SignInAsked {
		if d == id {
			return true
		}
	}
	return false
}

// markSignInAsked remembers that a source's sign-in was asked for, so it
// isn't asked again.
func (a *app) markSignInAsked(id string) {
	if a.signInAsked(id) {
		return
	}
	a.settings.SignInAsked = append(a.settings.SignInAsked, id)
	a.settings.save()
}

// startSignIn asks the showing source's plugin to sign the listener in,
// which may open its sign-in page. Done, the account and the tabs load anew,
// the favorites among them.
func (a *app) startSignIn() {
	p := a.sourceEntry.plugin
	if p == nil || !p.Can("signIn") || a.signInBusy {
		return
	}
	a.signInBusy = true
	a.signInNote = ""
	go func() {
		err := p.SignIn()
		if err != nil {
			log.Printf("sign-in %s: %v", p.Info().ID, err)
		}
		a.post(func() {
			a.signInBusy = false
			if err != nil {
				a.signInNote = videoProblem(err, i18n.T("Couldn't sign in"))
				a.askSignIn = true
				return
			}
			a.signInNote = ""
			a.loadAccount(false)
			a.loadTabs()
			a.loadTab()
		})
	}()
}

// ---- the tabs and the grid ----

// loadTabs asks the source what its chips are. A source answers the chips it
// can even when it couldn't ask the site for all of them — its own ranks
// come anyway — so what came back always shows; only nothing at all is an
// error worth owning.
func (a *app) loadTabs() {
	src := a.sourceEntry
	if src == nil || !src.src.Can("videoTabs") {
		a.tabs, a.tabsLoading, a.tabsErr = nil, false, ""
		a.tabID = ""
		return
	}
	if a.tabsLoading {
		return
	}
	a.tabsLoading = true
	a.tabsErr = ""
	go func() {
		tabs, err := src.src.Tabs()
		a.post(func() {
			a.tabsLoading = false
			if len(tabs) > 0 {
				a.tabs = tabs
				if !a.hasTab(a.tabID) {
					a.tabID = tabs[0].ID
					a.loadTab()
				}
				return
			}
			if err != nil {
				log.Printf("tabs %s: %v", src.id, err)
				a.tabsErr = videoProblem(err, i18n.T("Couldn\u2019t load this source\u2019s categories"))
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
// From a drama's page or the player, it steps back to the list first. The
// local source's add tab asks for a folder instead of listing one.
func (a *app) useTab(id string) {
	if id == "add" && a.sourceEntry != nil && a.sourceEntry.id == local.ID {
		a.addLocalFolder()
		return
	}
	for a.page != pageList {
		a.back()
	}
	if a.tabID == id && !a.searching {
		if len(a.items) == 0 && !a.gridLoading {
			a.loadTab()
		}
		return
	}
	a.tabID = id
	a.searching = false
	a.playlistQuery = ""
	a.clearGrid()
	a.loadTab()
}

// isPlaylistTab reports whether tabID is one of the listener's own playlists:
// their favorites folders, their saved collections, their watch-later list,
// or a folder of the local source.
func isPlaylistTab(id string) bool {
	return id == "watchlater" || strings.HasPrefix(id, "fav:") || strings.HasPrefix(id, "season:") ||
		strings.HasPrefix(id, "dir:")
}

// addLocalFolder asks for a folder and takes it into the local source, as a
// group of its own. A folder already there is left as it is.
func (a *app) addLocalFolder() {
	paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
		Title:     i18n.T("Choose a folder of videos"),
		Directory: true,
	})
	if err != nil || len(paths) == 0 || a.localSrc == nil {
		return
	}
	dir := paths[0]
	for _, have := range a.localSrc.Folders {
		if have == dir {
			return
		}
	}
	a.localSrc.Folders = append(a.localSrc.Folders, dir)
	a.settings.LocalFolders = append([]string(nil), a.localSrc.Folders...)
	a.settings.save()
	a.tabID = "dir:" + dir
	a.loadTabs()
	a.loadTab()
}

// currentTabLabel returns the showing tab's human label.
func (a *app) currentTabLabel() string {
	for _, t := range a.tabs {
		if t.ID == a.tabID {
			return a.tabLabel(t)
		}
	}
	return a.tabID
}

// playlistItems returns the showing playlist's items, filtered by
// playlistQuery when the listener typed into the playlist search field.
func (a *app) playlistItems() []video.Item {
	q := strings.ToLower(strings.TrimSpace(a.playlistQuery))
	if q == "" {
		return a.items
	}
	var out []video.Item
	for _, it := range a.items {
		if strings.Contains(strings.ToLower(it.Title), q) || strings.Contains(strings.ToLower(it.Subtitle), q) {
			out = append(out, it)
		}
	}
	return out
}

// setQueue remembers a playlist's items as the active playback queue.
func (a *app) setQueue(items []video.Item, at int, title string) {
	if len(items) == 0 {
		a.clearQueue()
		return
	}
	a.queue = append([]video.Item(nil), items...)
	if at < 0 || at >= len(a.queue) {
		at = 0
	}
	a.queueAt = at
	a.queueTitle = title
}

// clearQueue forgets the playlist queue.
func (a *app) clearQueue() {
	a.queue = nil
	a.queueAt = 0
	a.queueTitle = ""
}

// firstEpisodeOf makes the initial episode descriptor for playing an item
// straight from a card or a playlist before its full detail comes back.
func firstEpisodeOf(it video.Item) video.Episode {
	if it.Source == "huangguo" {
		return video.Episode{ID: "1"}
	}
	return video.Episode{ID: it.ID, Title: it.Title}
}

// playPlaylist plays a list of videos in order or shuffled, starting at start.
func (a *app) playPlaylist(items []video.Item, start int, shuffle bool) {
	if len(items) == 0 {
		return
	}
	list := append([]video.Item(nil), items...)
	if shuffle {
		rand.Shuffle(len(list), func(i, j int) { list[i], list[j] = list[j], list[i] })
		start = 0
	}
	if start < 0 || start >= len(list) {
		start = 0
	}
	a.detailOf = ""
	a.setQueue(list, start, a.currentTabLabel())
	a.playQueueAt(start)
}

// playPlaylistOnCodie plays a playlist directly on the paired codie device
// from start, using the currently selected character slot without a modal.
func (a *app) playPlaylistOnCodie(items []video.Item, start int) {
	if len(items) == 0 || a.codie == nil {
		return
	}
	if start < 0 || start >= len(items) {
		start = 0
	}
	a.detailOf = ""
	a.setQueue(items, start, a.currentTabLabel())
	a.deviceOutput = true
	a.playQueueAt(start)
}

// playQueueAt plays the video at idx in the active playlist queue.
func (a *app) playQueueAt(idx int) {
	if idx < 0 || idx >= len(a.queue) {
		return
	}
	a.queueAt = idx
	it := a.queue[idx]
	if a.detailOf != "" {
		a.detailOf = it.ID
	}
	a.playEpisode(it, firstEpisodeOf(it), 0)
}

// playItemFromGrid plays one card directly from the grid: if the grid is a
// playlist, the rest of the playlist follows in the queue.
func (a *app) playItemFromGrid(items []video.Item, idx int) {
	if idx < 0 || idx >= len(items) {
		return
	}
	if isPlaylistTab(a.tabID) && !a.searching {
		a.playPlaylist(items, idx, false)
		return
	}
	a.detailOf = ""
	a.clearQueue()
	it := items[idx]
	a.playEpisode(it, firstEpisodeOf(it), 0)
}

// openAndPlayTab switches to a playlist tab, loads its items, and starts
// playing from the first one as soon as they arrive.
func (a *app) openAndPlayTab(tabID string) {
	src := a.sourceEntry
	if src == nil {
		return
	}
	a.useTab(tabID)
	go func() {
		l, err := src.src.List(tabID, 1)
		a.post(func() {
			if err != nil || l == nil || len(l.Items) == 0 {
				return
			}
			for i := range l.Items {
				if l.Items[i].Source == "" {
					l.Items[i].Source = src.id
				}
			}
			a.items = l.Items
			a.gridPage = 1
			a.gridMore = l.More
			a.gridLoading = false
			a.playPlaylist(l.Items, 0, false)
		})
	}()
}

// clearGrid forgets what the grid holds.
func (a *app) clearGrid() {
	a.items, a.gridPage, a.gridMore = nil, 0, false
	a.gridLoading, a.gridErr = false, ""
}

// loadTab loads the first of what the tab lists, asking anew. A cold source
// gets one ask again on its own.
func (a *app) loadTab() {
	a.clearGrid()
	a.retried = false
	a.loadMore()
}

// loadMore loads the next page of what the tab lists into the grid, by the
// source's own counting: pages from one.
func (a *app) loadMore() {
	src := a.sourceEntry
	if src == nil || a.gridLoading || a.searching || a.tabsErr != "" {
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
				// A plugin just started sometimes fails its first ask —
				// its scripts are still warming. Ask once more before
				// owning up.
				if next == 1 && len(a.items) == 0 && !a.retried {
					a.retried = true
					a.gridLoading = true
					time.AfterFunc(1200*time.Millisecond, func() { a.post(a.loadMore) })
					return
				}
				// A later page that didn’t come keeps what already shows,
				// instead of clearing it for an error.
				if len(a.items) == 0 {
					a.gridErr = videoProblem(err, i18n.T("Couldn’t load this page"))
				}
				log.Printf("list %s/%s page %d: %v", src.id, a.tabID, next, err)
				if macBlocked(err) {
					a.macRetry = func() { a.loadTab() }
				}
				return
			}
			a.gridPage = next
			a.gridMore = l.More
			a.items = append(a.items, l.Items...)
			for i := range a.items {
				if a.items[i].Source == "" {
					a.items[i].Source = src.id
				}
			}
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
	if a.codie != nil && a.deviceSlot == 0 {
		a.syncDeviceSlot()
	}
	go func() {
		v, err := src.src.Video(item.ID)
		a.post(func() {
			if a.page != pageDetail || a.detailOf != item.ID {
				return
			}
			a.detailLoading = false
			if err != nil {
				log.Printf("video %s/%s: %v", src.id, item.ID, err)
				a.detailErr = videoProblem(err, i18n.T("Couldn’t load this drama"))
				if macBlocked(err) {
					a.macRetry = func() { a.openDetail(item) }
				}
				return
			}
			a.detail = v
		})
	}()
}

// playEpisode opens an episode in the player, of the source it came from.
// The drama's page comes along if it isn't there yet, so the drawer and the
// episode buttons work from the first second.
func (a *app) playEpisode(item video.Item, ep video.Episode, at int) {
	src := a.sourceOf(item.Source)
	if src == nil {
		src = a.sourceEntry
	}
	if src == nil {
		return
	}
	if item.Source == "" {
		item.Source = src.id
	}
	a.recordPlayProgress()
	a.cancelNext()
	if a.pipWin != nil {
		a.closePip()
	}
	a.stopPlayer()
	a.playerErr = ""
	a.controlsOn = true
	a.nextLatch = false
	a.playing = playing{item: item, ep: ep, at: at}
	if len(a.queue) > 0 && (a.queueAt < 0 || a.queueAt >= len(a.queue) || a.queue[a.queueAt].ID != item.ID) {
		for i := range a.queue {
			if a.queue[i].ID == item.ID {
				a.queueAt = i
				break
			}
		}
	}
	a.page = pagePlay
	// 接着看:上一次看到哪儿,这集从哪儿起。
	a.resumeAt = 0
	if p := a.lib.ProgressOf(item.ID); p != nil && p.EpID == ep.ID && p.Pos > 10 && !p.Done {
		a.resumeAt = p.Pos
	}
	// The drawer and the episode buttons need the drama whole; a play that
	// didn't come off the drama's page asks for it behind the stream.
	if a.detail == nil || a.detail.ID != item.ID {
		a.detail = &video.Video{Item: item}
		go func() {
			v, err := src.src.Video(item.ID)
			a.post(func() {
				if err != nil || v == nil || a.playing.item.ID != item.ID || a.page != pagePlay {
					return
				}
				a.detail = v
				if a.playing.at >= 0 && a.playing.at < len(v.Episodes) {
					a.playing.ep = v.Episodes[a.playing.at]
				}
			})
		}()
	}
	// 在 codie 上播放:取流后交给设备的媒体会话。
	if a.deviceOutput && a.codie != nil {
		resumeAt := a.resumeAt
		a.resumeAt = 0
		// 播放列表里预转好的这一集:不再取流、转码,直接放。
		if path, rate, ok := a.takeCodiePrepared(item, ep); ok {
			gen := a.codieGen
			log.Printf("codie play: playing the file readied for %s / %s", item.ID, ep.ID)
			go a.codieShow(path, rate, resumeAt, gen)
			return
		}
		a.codieBusy = i18n.T("Transcoding for the device…")
		go func() {
			log.Printf("codie play: resolving %s / %s", item.ID, ep.ID)
			s, err := src.src.Play(item.ID, ep.ID)
			if err != nil {
				log.Printf("codie play: resolve failed: %v", err)
				a.post(func() {
					a.codieBusy = ""
					a.page = pagePlay
					a.playing = playing{item: item, ep: ep, at: at}
					a.playerErr = videoProblem(err, i18n.T("This episode won’t play"))
				})
				return
			}
			log.Printf("codie play: resolved (%s, audio=%v)", s.Type, s.AudioURL != "")
			// 转码和上传都在这条 goroutine 上,主线程只收结果。
			a.playEpisodeOnDevice(item, ep, at, s, resumeAt, func() (*video.Stream, error) {
				return src.src.Play(item.ID, ep.ID)
			})
		}()
		return
	}
	a.lib.Watch(item, ep, at, 0, 0, false)
	go func() {
		s, err := src.src.Play(item.ID, ep.ID)
		if err != nil {
			log.Printf("play %s/%s: %v", item.Source, item.ID, err)
			if macBlocked(err) {
				a.post(func() {
					a.macRetry = func() { a.playEpisode(item, ep, at) }
					a.playerErr = videoProblem(err, i18n.T("This episode won’t play"))
				})
				return
			}
			a.post(func() { a.playerErr = videoProblem(err, i18n.T("This episode won\u2019t play")) })
			return
		}
		// Where the story was left goes into the pass itself when the stream is
		// made on the fly; an address or a file the player opens seeks after.
		startS := 0.0
		if a.resumeAt > 2 {
			startS = a.resumeAt - 2
		}
		p, file, baked, err := openEpisodeStream(s, startS)
		a.post(func() {
			if a.playing.item.ID != item.ID || a.playing.at != at || a.page != pagePlay {
				if p != nil {
					stopStream(p)
					p.Close()
				}
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
			if a.rate != 1 {
				p.SetRate(a.rate)
			}
			if a.resumeAt > 0 {
				if !baked {
					p.Seek(time.Duration((a.resumeAt - 2) * float64(time.Second)))
				}
				a.resumeAt = 0
			}
			a.startWatchTicker()
		})
	}()
}

// recordPlayProgress writes how far the episode on now is into the library.
func (a *app) recordPlayProgress() {
	if a.playing.item.ID == "" || a.lib == nil {
		return
	}
	var pos, dur float64
	done := false
	if a.deviceOutput {
		// The device's clock is the file's, which runs rate times faster
		// than the story; what's kept of the story is story time.
		p, d := a.codieTimes()
		pos, dur = p.Seconds()*a.rate, d.Seconds()*a.rate
	} else if p := a.player; p != nil && p.Ready() && p.Err() == nil {
		pos, dur = p.Position().Seconds(), p.Duration().Seconds()
	}
	if dur > 0 && pos >= dur-1.5 {
		done = true
	}
	a.lib.Watch(a.playing.item, a.playing.ep, a.playing.at, pos, dur, done)
}

// startWatchTicker records the progress now and again, for an episode that
// plays a long while — here or on the device.
func (a *app) startWatchTicker() {
	a.stopWatchTicker()
	a.watchTimer = time.AfterFunc(10*time.Second, func() {
		a.post(func() {
			local := a.player != nil && a.player.Playing()
			device := a.deviceOutput && a.codieSession != ""
			if !local && !device {
				return
			}
			a.recordPlayProgress()
			a.startWatchTicker()
		})
	})
}

// stopWatchTicker ends the recording of the progress.
func (a *app) stopWatchTicker() {
	if a.watchTimer != nil {
		a.watchTimer.Stop()
		a.watchTimer = nil
	}
}

// startNextCountdown counts the seconds to the next episode, showing them on
// the stage, and plays it when they run out.
func (a *app) startNextCountdown() {
	if a.nextTimer != nil || !a.hasEpisode(1) || a.settings == nil || a.settings.NoAutoNext {
		return
	}
	a.nextLeft = 8
	if a.win != nil {
		a.win.Invalidate()
	}
	a.nextTimer = time.AfterFunc(time.Second, func() { a.post(a.tickNext) })
}

// tickNext is one second less to the next episode.
func (a *app) tickNext() {
	a.nextTimer = nil
	if a.page != pagePlay || a.nextLeft <= 1 {
		if a.nextLeft <= 1 && a.page == pagePlay {
			a.nextLeft = 0
			a.nextEpisode(false)
			return
		}
		a.nextLeft = 0
		return
	}
	a.nextLeft--
	a.win.Invalidate()
	a.nextTimer = time.AfterFunc(time.Second, func() { a.post(a.tickNext) })
}

// cancelNext takes the countdown to the next episode away.
func (a *app) cancelNext() {
	if a.nextTimer != nil {
		a.nextTimer.Stop()
		a.nextTimer = nil
	}
	a.nextLeft = 0
}

// nextEpisode opens the episode after the one playing, or the one before; when
// a playlist queue is playing and the current video has no more episodes in
// that direction, it moves on to the next or previous video in the playlist.
func (a *app) nextEpisode(back bool) {
	dir := 1
	if back {
		dir = -1
	}
	if d := a.detail; d != nil {
		at := a.playing.at + dir
		if at >= 0 && at < len(d.Episodes) {
			a.playEpisode(d.Item, d.Episodes[at], at)
			return
		}
	}
	if len(a.queue) > 1 {
		qat := a.queueAt + dir
		if qat >= 0 && qat < len(a.queue) {
			a.playQueueAt(qat)
		}
	}
}

// stopPlayer ends what plays, and its file — here or on the device.
func (a *app) stopPlayer() {
	a.cancelNext()
	a.stopWatchTicker()
	if a.controlsTmr != nil {
		a.controlsTmr.Stop()
		a.controlsTmr = nil
	}
	a.stopPlayerForCodie()
	p, file := a.player, a.playerFile
	a.player, a.playerFile = nil, ""
	if p != nil {
		// A stream fed by ffmpeg ends with its player, or the pass runs on
		// with nothing watching it.
		stopStream(p)
		p.Close()
	}
	if file != "" {
		go os.Remove(file)
	}
}

// setRate plays the episode at r times the usual speed, now and from now on.
func (a *app) setRate(r float64) {
	old := a.rate
	a.rate = r
	a.settings.Rate = r
	a.saveSettingsSoon()
	// On the device the pace is baked into the file, so a change means the
	// episode plays again at the new one, from where the story is.
	if a.deviceOutput && a.codie != nil && a.codieSession != "" && r != old {
		go a.codieRespeed(old, r)
		return
	}
	if p := a.player; p != nil {
		p.SetRate(r)
	}
}

// setVolume sets how loud the episode plays, and keeps it for next time.
func (a *app) setVolume(v float64) {
	a.vol = v
	a.settings.Volume = v
	a.saveSettingsSoon()
}

// saveSettingsSoon writes the settings once the changes settle.
func (a *app) saveSettingsSoon() {
	if a.saveTmr != nil {
		a.saveTmr.Reset(600 * time.Millisecond)
		return
	}
	a.saveTmr = time.AfterFunc(600*time.Millisecond, func() {
		a.post(func() {
			a.saveTmr = nil
			if a.settings != nil {
				a.settings.save()
			}
		})
	})
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

// macBlocked says whether an error is macOS standing between a plugin and
// the program it started — its "operation not permitted", which a fresh ask
// may be allowed past. The plugin's answer carries the message alone, so
// the words are what there is to go by.
func macBlocked(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "operation not permitted")
}

// settings is what the app keeps of the viewer's.
type settings struct {
	Source string `json:"source,omitempty"`
	// Volume is how loud, from 0 to 100; Rate, how many times the usual
	// speed. Both carry from one episode to the next, and one run to the
	// next.
	Volume float64 `json:"volume,omitempty"`
	Rate   float64 `json:"rate,omitempty"`
	// NoAutoNext stops the next episode from coming on its own when one
	// ends.
	NoAutoNext bool `json:"noAutoNext,omitempty"`
	// MaxHeight is the tallest picture a source may hand over, 0 for the
	// best it has: 1080, 720 and 480 are the choices.
	MaxHeight int `json:"maxHeight,omitempty"`
	// Subtitles burns the source's subtitles into the picture when it has
	// some. The player has no track of its own, so it's the file that carries
	// them.
	Subtitles bool `json:"subtitles,omitempty"`
	// ConversationControl lets the paired codie device's talk move the
	// playback: what the viewer says to it ("next one", "play …") lands
	// here as tool calls.
	ConversationControl bool `json:"conversationControl,omitempty"`
	// SearchHistory is the last queries, newest first, as chips under the
	// search field.
	SearchHistory []string `json:"searchHistory,omitempty"`
	// WizardDone stops the first run's hello from coming again.
	WizardDone bool `json:"wizardDone,omitempty"`
	// UpdatesChecked is when the repo was last asked for a newer release.
	UpdatesChecked time.Time `json:"updatesChecked,omitzero"`
	// DisabledPlugins are the ids of the plugins turned off.
	DisabledPlugins []string `json:"disabledPlugins,omitempty"`
	// Appearance is the palette the app draws with: system, dark or light.
	Appearance string `json:"appearance,omitempty"`
	// Proxy is the HTTP proxy the app's asks to the world go through,
	// "10.0.0.14:7890" or "http://10.0.0.14:7890", empty for none. The
	// paired device and the intranet behind it stay direct however it is.
	Proxy string `json:"proxy,omitempty"`
	// LocalFolders are the folders the local source plays, each one a group,
	// in the order the viewer added them.
	LocalFolders []string `json:"localFolders,omitempty"`
	// DisabledSources are the built-in sources turned off. Kept so an older
	// settings file still reads; Huangguo is a plugin now, and lives in
	// DisabledPlugins.
	DisabledSources []string `json:"disabledSources,omitempty"`
	// SourcesChosen marks a settings file that names its sources, so a first
	// run can start Huangguo off without turning it off again later.
	SourcesChosen bool `json:"sourcesChosen,omitempty"`
	// SignInAsked are the sources whose sign-in was already asked for. A
	// source asks once; after that the sidebar's own button is the way in.
	SignInAsked []string `json:"signInAsked,omitempty"`
	// Language is what the app's words read in: system, en or zh-CN.
	Language string `json:"language,omitempty"`
	path     string
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
	s.path = filepath.Join(userDataDir(""), "settings.json")
	if b, err := os.ReadFile(s.path); err == nil {
		// An editor on Windows may leave a UTF-8 BOM in front, which JSON
		// doesn't take: without this the whole file reads as nothing and the
		// settings go back to their defaults.
		b = bytes.TrimPrefix(b, []byte("\xef\xbb\xbf"))
		json.Unmarshal(b, s)
	}
	// A first run starts Huangguo off. It still ships with the app, as a
	// plugin, and the settings turn it on. An older file that turned the
	// built-in source off keeps it off, now as a plugin.
	if !s.SourcesChosen {
		if !containsString(s.DisabledPlugins, "huangguo") {
			s.DisabledPlugins = append(s.DisabledPlugins, "huangguo")
		}
		// 第一次跑:720p 够用了;对着设备说话就能控制这里的播放,默认开。
		s.MaxHeight = 720
		s.ConversationControl = true
		s.SourcesChosen = true
		s.save()
	} else if containsString(s.DisabledSources, "huangguo") && !containsString(s.DisabledPlugins, "huangguo") {
		s.DisabledPlugins = append(s.DisabledPlugins, "huangguo")
		s.save()
	}
	return s
}

// containsString reports whether list holds s.
func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// pluginsDir is where plugins are dropped in.
func pluginsDir() string { return userDataDir("plugins") }

// userDataDir is where the app keeps its own: redirected to a folder of its
// own when the run exists only to be looked at.
func userDataDir(sub string) string {
	dir := ""
	if shotsMode {
		dir = filepath.Join(os.TempDir(), "codie-video-player-shots")
	} else if d, err := mygo.App.Path(mygo.PathUserData); err == nil {
		dir = d
	} else {
		dir = filepath.Join(os.TempDir(), "codie-video-player")
	}
	return filepath.Join(dir, sub)
}

// setLanguage speaks what the settings choose, or, for system, what the
// system prefers — the plugins' translations and the app's own words alike.
func (a *app) setLanguage() {
	i18n.SetLanguage(i18n.Resolve(a.settings.Language, mygo.App.PreferredLanguages()))
}

// rethemeWindows redraws every window, for a change of appearance or
// language: each picks the palette and the words up as it draws again.
func (a *app) rethemeWindows() {
	a.applyPalette()
	if a.win != nil {
		a.win.Invalidate()
	}
	if a.settingsWin != nil {
		a.settingsWin.Invalidate()
	}
	if a.pipWin != nil {
		a.pipWin.Invalidate()
	}
	if a.deviceWin != nil {
		a.deviceWin.Invalidate()
	}
}
