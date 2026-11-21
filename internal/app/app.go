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
	"time"

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
	tabsErr     string // the source gave no chips at all, and why
	query       string // what's in the search field
	searchBox   ui.Handle
	searching   bool // the grid shows what a search found
	items       []video.Item
	gridPage    int // the last page loaded, 0 for none
	gridLoading bool
	gridErr     string
	retried     bool // a cold source's first ask already asked again

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
	rate          float64
	fullscreen    bool
	drawerOpen    bool
	speedOpen     bool
	nextLeft      int         // seconds to the next episode, 0 for none
	nextTimer     *time.Timer // counting it down
	nextLatch     bool        // this episode already counted down once
	resumeAt      float64     // seconds into the episode it opens at
	watchTimer    *time.Timer // records how far the episode is, now and then
	pipWin        *mygo.Window
	epsList       ui.ListState // the drawer's long list of episodes

	// The viewer's own: follows, history, and how far each drama is.
	lib *library

	// Where a back can go forward to.
	fwd []location

	// The controls over the picture: whether they show, and when to take
	// them away again.
	controlsOn  bool
	controlsAt  time.Time
	controlsTmr *time.Timer

	// A search's answers, one group to a source, and which of them shows.
	searchGroups []searchGroup
	searchFilter string // "all", or one source's id

	// An update, when the repo has one newer than the app.
	updateTag     string
	updateURL     string
	updateChecking bool

	saveTmr  *time.Timer // the settings' write, once the changes settle
	quitting bool       // a real quit is on: the window closes instead of hiding

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
	a := &app{vol: 100, bundled: bundledPlugins}
	current = a
	a.covers = newCovers(a.post)
	mygo.App.WhenReady(func() {
		a.settings = loadSettings()
		a.lib = loadLibrary(userDataDir(""))
		if a.settings.Appearance == "" {
			a.settings.Appearance = AppearanceSystem
		}
		if a.settings.Language == "" {
			a.settings.Language = i18n.System
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
		return "", errors.New(i18n.T("This app doesn\u2019t sign in to accounts"))
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
	a.searchGroups = nil
	a.query = ""
	a.tabID = ""
	a.tabs, a.tabsLoading = nil, false
	a.fwd = nil
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
				a.gridErr = videoProblem(err, i18n.T("Couldn’t load this page"))
				return
			}
			a.gridPage = next
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
	go func() {
		v, err := src.src.Video(item.ID)
		a.post(func() {
			if a.page != pageDetail || a.detailOf != item.ID {
				return
			}
			a.detailLoading = false
			if err != nil {
				a.detailErr = videoProblem(err, i18n.T("Couldn’t load this drama"))
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
	a.page = pagePlay
	// 接着看:上一次看到哪儿,这集从哪儿起。
	a.resumeAt = 0
	if p := a.lib.progressOf(item.ID); p != nil && p.EpID == ep.ID && p.Pos > 10 && !p.Done {
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
			})
		}()
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
					a.playerErr = videoProblem(err, i18n.T("This episode won\u2019t play"))
				})
				return
			}
			log.Printf("codie play: resolved (%s, audio=%v)", s.Type, s.AudioURL != "")
			// 转码和上传都在这条 goroutine 上,主线程只收结果。
			go a.playEpisodeOnDevice(item, ep, at, s)
		}()
		return
	}
	a.lib.watch(item, ep, at, 0, 0, false)
	go func() {
		s, err := src.src.Play(item.ID, ep.ID)
		if err != nil {
			a.post(func() { a.playerErr = videoProblem(err, i18n.T("This episode won\u2019t play")) })
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
			if a.rate != 1 {
				p.SetRate(a.rate)
			}
			if a.resumeAt > 0 {
				p.Seek(time.Duration((a.resumeAt - 2) * float64(time.Second)))
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
		p, d := a.codieTimes()
		pos, dur = p.Seconds(), d.Seconds()
	} else if p := a.player; p != nil && p.Ready() && p.Err() == nil {
		pos, dur = p.Position().Seconds(), p.Duration().Seconds()
	}
	if dur > 0 && pos >= dur-1.5 {
		done = true
	}
	a.lib.watch(a.playing.item, a.playing.ep, a.playing.at, pos, dur, done)
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
	if a.nextTimer != nil || !a.hasEpisode(1) || a.settings.NoAutoNext {
		return
	}
	a.nextLeft = 8
	a.win.Invalidate()
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
		p.Close()
	}
	if file != "" {
		go os.Remove(file)
	}
}

// setRate plays the episode at r times the usual speed, now and from now on.
func (a *app) setRate(r float64) {
	a.rate = r
	a.settings.Rate = r
	a.saveSettingsSoon()
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
		json.Unmarshal(b, s)
	}
	return s
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
