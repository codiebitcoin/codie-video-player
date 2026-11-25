// Package web is the browser face of the player: the same sources the
// desktop app knows, over HTTP — browse, search, and watch in the page. It
// borrows the family's server shape (codie-life-connect): a plain net/http
// mux, a cookie login, the web app embedded and served as files, and the
// state guarded by locks instead of a UI thread.
package web

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"codie-video-player/internal/i18n"
	"codie-video-player/internal/library"
	"codie-video-player/internal/plugin"
	"codie-video-player/internal/source/local"
	"codie-video-player/internal/video"
)

// Options is how the server is built.
type Options struct {
	DataDir    string // settings, library, transcode temp
	PluginsDir string // plugin folders
	Version    string
	AuthUser   string // empty user or pass makes a random pair, logged once
	AuthPass   string
	Static     fs.FS // the built web app, at / when logged in
}

// source is one place videos come from.
type source struct {
	id   string
	name string
	src  video.VideoSource
}

// Server is the whole web app.
type Server struct {
	dataDir  string
	version  string
	sources  []source
	plugins  []*plugin.Plugin
	localSrc *local.Source
	settings *settings
	lib      *library.Library
	auth     *auth
	sessions *sessionManager
	covers   *coverCache
	static   fs.FS
}

// New assembles the server: the built-in source, the plugins of the folder,
// the library, and the rest. It doesn't listen yet.
func New(opt Options) (*Server, error) {
	s := &Server{
		dataDir: opt.DataDir,
		version: opt.Version,
		lib:     library.Load(opt.DataDir),
		auth:    newAuth(opt.AuthUser, opt.AuthPass),
		static:  opt.Static,
	}
	s.settings = loadSettings(opt.DataDir)
	i18n.SetLanguage(s.settings.Language)
	// Plugins talk like the desktop's: no account sign-ins here either, and
	// the version they ask about is ours.
	plugin.SetOpenURL(func(string) error { return nil })
	plugin.SetAppVersion(opt.Version)
	plugin.SetWebSignIn(func(plugin.WebSignIn) (string, error) {
		return "", errors.New(i18n.T("This app doesn't sign in to accounts"))
	})
	dataDir := filepath.Join(opt.DataDir, "plugin-data")
	for _, p := range plugin.Discover(opt.PluginsDir, dataDir) {
		// A server runner has already said yes to what plugins run; the
		// desktop's grant banner has no page here.
		if len(p.Permissions().Process) > 0 && !p.Granted() {
			if err := p.Grant(true); err != nil {
				log.Printf("web: grant %s: %v", p.Info().ID, err)
			} else {
				log.Printf("web: granted process permission to %s", p.Info().ID)
			}
		}
		s.plugins = append(s.plugins, p)
			if s.pluginDisabled(p.Info().ID) {
				continue
			}
			if v := p.Video(); v != nil {
				s.sources = append(s.sources, source{id: v.Info().ID, name: v.Info().Name, src: v})
			}
		}
		s.localSrc = &local.Source{Folders: append([]string(nil), s.settings.LocalFolders...)}
		s.sources = append(s.sources, source{id: local.ID, name: i18n.T("Local"), src: s.localSrc})
		s.orderSources()

	var err error
	if s.sessions, err = newSessionManager(filepath.Join(opt.DataDir, "tmp")); err != nil {
		return nil, err
	}
	s.covers = newCoverCache(s.pluginHeaders)
	return s, nil
}

// orderSources puts the dropdown in the desktop's order: local folders, then
// YouTube, then Bilibili, then NicoNico, then the other plugins, Huangguo last.
func (s *Server) orderSources() {
	slices.SortStableFunc(s.sources, func(x, y source) int {
		rank := func(e source) int {
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
}

// pluginDisabled reports whether a plugin is turned off.
func (s *Server) pluginDisabled(id string) bool {
	for _, d := range s.settings.DisabledPlugins {
		if d == id {
			return true
		}
	}
	return false
}

// Handler is everything the server answers, in one handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		s.writeJSON(w, map[string]any{"ok": true, "version": s.version})
	})
	mux.HandleFunc("POST /api/login", s.auth.handleLogin)
	mux.HandleFunc("POST /api/logout", s.auth.handleLogout)

	api := http.NewServeMux()
	api.HandleFunc("GET /api/sources", s.handleSources)
	api.HandleFunc("GET /api/source/{id}/tabs", s.handleTabs)
	api.HandleFunc("GET /api/source/{id}/list", s.handleList)
	api.HandleFunc("GET /api/search", s.handleSearch)
	api.HandleFunc("GET /api/video/{src}/{id}", s.handleVideo)
	api.HandleFunc("GET /api/cover", s.handleCover)
	api.HandleFunc("POST /api/play", s.handlePlay)
	api.HandleFunc("GET /api/session/{id}", s.handleSessionState)
	api.HandleFunc("DELETE /api/session/{id}", s.handleSessionStop)
	api.HandleFunc("POST /api/session/{id}/progress", s.handleProgress)
	api.HandleFunc("GET /api/session/{id}/", s.handleSessionFiles)
	api.HandleFunc("GET /api/history", s.handleHistory)
		api.HandleFunc("GET /api/follows", s.handleFollows)
		api.HandleFunc("POST /api/follow", s.handleFollow)
		api.HandleFunc("GET /api/settings", s.handleSettingsGet)
		api.HandleFunc("POST /api/settings", s.handleSettingsPost)
		api.HandleFunc("POST /api/local/folder", s.handleLocalFolder)
		mux.Handle("/api/", s.auth.guard(api))
	// The web app's shell is public — it has nothing in it; the viewer's
	// data lives behind the API, and the page itself shows the login when
	// an ask comes back 401.
	mux.Handle("/", s.staticHandler())
	return mux
}

// staticHandler serves the built web app, falling back to its index for the
// paths the app routes itself.
func (s *Server) staticHandler() http.Handler {
	file := http.FileServer(http.FS(s.static))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/")
		if p == "" {
			p = "index.html"
		}
		if _, err := fs.Stat(s.static, p); err != nil {
			r.URL.Path = "/"
		}
		file.ServeHTTP(w, r)
	})
}

// ---- the sources' API ----

func (s *Server) handleSources(w http.ResponseWriter, r *http.Request) {
	out := make([]map[string]any, 0, len(s.sources))
	for _, so := range s.sources {
		out = append(out, map[string]any{
			"id":     so.id,
			"name":   so.name,
			"search": so.src.Can("videoSearch"),
		})
	}
	s.writeJSON(w, map[string]any{"sources": out, "current": s.settings.Source})
}

func (s *Server) sourceOf(id string) *source {
	for i := range s.sources {
		if s.sources[i].id == id {
			return &s.sources[i]
		}
	}
	return nil
}

func (s *Server) handleTabs(w http.ResponseWriter, r *http.Request) {
	so := s.sourceOf(r.PathValue("id"))
	if so == nil {
		http.Error(w, `{"error":"no such source"}`, http.StatusNotFound)
		return
	}
	tabs, err := so.src.Tabs()
	if err != nil {
		s.writeJSON(w, map[string]any{"tabs": []video.Tab{}, "error": videoProblem(err, i18n.T("Couldn’t load this source’s categories"))})
		return
	}
	if tabs == nil {
		tabs = []video.Tab{}
	}
	// The local source's chips are English keys; a plugin's come translated.
	if so.id == local.ID {
		for i := range tabs {
			tabs[i].Label = i18n.T(tabs[i].Label)
		}
	}
	s.writeJSON(w, map[string]any{"tabs": tabs})
}

func (s *Server) handleList(w http.ResponseWriter, r *http.Request) {
	so := s.sourceOf(r.PathValue("id"))
	if so == nil {
		http.Error(w, `{"error":"no such source"}`, http.StatusNotFound)
		return
	}
	tab := r.URL.Query().Get("tab")
	page := atoiDefault(r.URL.Query().Get("page"), 1)
	list, err := so.src.List(tab, page)
	if err != nil {
		s.writeJSON(w, map[string]any{"items": []video.Item{}, "error": videoProblem(err, i18n.T("Couldn’t load this page"))})
		return
	}
	if list == nil {
		list = &video.List{}
	}
	s.writeJSON(w, map[string]any{"items": list.Items, "more": list.More})
}

// searchGroup is one source's answers to a search.
type searchGroup struct {
	Source string       `json:"source"`
	Name   string       `json:"name"`
	Items  []video.Item `json:"items"`
	Error  string       `json:"error,omitempty"`
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		s.writeJSON(w, map[string]any{"groups": []searchGroup{}})
		return
	}
	which := r.URL.Query().Get("src")
	var group []source
	for i := range s.sources {
		if !s.sources[i].src.Can("videoSearch") {
			continue
		}
		if which != "" && which != "all" && which != s.sources[i].id {
			continue
		}
		group = append(group, s.sources[i])
	}
	// 一个源卡住不能拖死整个搜索:给个期限,到点没答的先空着。
	begin := time.Now()
	log.Printf("search %q: %d sources asked", q, len(group))
	done := make(chan struct{}, len(group))
	out := make([]searchGroup, len(group))
	for i, so := range group {
		go func(i int, so source) {
			g := searchGroup{Source: so.id, Name: so.name}
			res, err := so.src.Search(q, 0)
			if err != nil {
				g.Error = videoProblem(err, i18n.T("Search failed"))
			} else if res != nil {
				g.Items = res.Items
				for j := range g.Items {
					if g.Items[j].Source == "" {
						g.Items[j].Source = so.id
					}
				}
			}
			out[i] = g
			done <- struct{}{}
		}(i, so)
	}
	limit := time.After(searchDeadline)
	for range group {
		select {
		case <-done:
		case <-limit:
			goto gathered
		}
	}
gathered:
	log.Printf("search %q: answered in %s", q, time.Since(begin))
	s.writeJSON(w, map[string]any{"groups": out})
}

func (s *Server) handleVideo(w http.ResponseWriter, r *http.Request) {
	so := s.sourceOf(r.PathValue("src"))
	if so == nil {
		http.Error(w, `{"error":"no such source"}`, http.StatusNotFound)
		return
	}
	id := r.PathValue("id")
	v, err := so.src.Video(id)
	if err != nil {
		s.writeJSON(w, map[string]any{"error": videoProblem(err, i18n.T("Couldn’t load this drama"))})
		return
	}
	if v == nil {
		http.Error(w, `{"error":"no such drama"}`, http.StatusNotFound)
		return
	}
	out := map[string]any{"video": v, "followed": s.lib.Followed(id)}
	if p := s.lib.ProgressOf(id); p != nil {
		out["progress"] = p
	}
	s.writeJSON(w, out)
}

// ---- the viewer's own ----

func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request) {
	src := r.URL.Query().Get("src")
	var history []library.Watched
	if src == "" || src == "all" {
		_, history = s.lib.Snapshot()
	} else {
		_, history = s.lib.Of(src)
	}
	if history == nil {
		history = []library.Watched{}
	}
	s.writeJSON(w, map[string]any{"history": history})
}

func (s *Server) handleFollows(w http.ResponseWriter, r *http.Request) {
	src := r.URL.Query().Get("src")
	var follows []library.Watched
	if src == "" || src == "all" {
		follows, _ = s.lib.Snapshot()
	} else {
		follows, _ = s.lib.Of(src)
	}
	if follows == nil {
		follows = []library.Watched{}
	}
	s.writeJSON(w, map[string]any{"follows": follows})
}

func (s *Server) handleSettingsGet(w http.ResponseWriter, r *http.Request) {
	s.writeJSON(w, map[string]any{
		"language":        s.settings.Language,
		"source":          s.settings.Source,
		"localFolders":    s.settings.LocalFolders,
		"disabledPlugins": s.settings.DisabledPlugins,
		"plugins":         s.pluginList(),
		"languages": []map[string]string{
			{"code": i18n.English, "name": "English"},
			{"code": i18n.Chinese, "name": "简体中文"},
			{"code": i18n.Japanese, "name": "日本語"},
		},
	})
}

func (s *Server) handleSettingsPost(w http.ResponseWriter, r *http.Request) {
		var q struct {
		Language        *string  `json:"language"`
		Source          *string  `json:"source"`
		DisabledPlugins []string `json:"disabledPlugins"`
	}
	if err := json.NewDecoder(r.Body).Decode(&q); err != nil {
		http.Error(w, `{"error":"bad request"}`, http.StatusBadRequest)
		return
	}
	if q.Language != nil {
		lang := *q.Language
		if lang != i18n.English && lang != i18n.Chinese && lang != i18n.Japanese {
			lang = i18n.English
		}
		s.settings.Language = lang
		i18n.SetLanguage(lang)
		for i := range s.sources {
			if s.sources[i].id == local.ID {
				s.sources[i].name = i18n.T("Local")
			}
		}
	}
	if q.Source != nil && s.sourceOf(*q.Source) != nil {
		s.settings.Source = *q.Source
	}
		if q.DisabledPlugins != nil {
			s.settings.DisabledPlugins = q.DisabledPlugins
			s.rebuildSources()
		}
	s.settings.save()
	s.handleSettingsGet(w, r)
}

// pluginList is every loaded plugin, on or off, for the settings page.
func (s *Server) pluginList() []map[string]string {
	out := make([]map[string]string, 0, len(s.plugins))
	for _, p := range s.plugins {
		info := p.Info()
		out = append(out, map[string]string{"id": info.ID, "name": info.Name, "version": info.Version})
	}
	return out
}

// rebuildSources puts the source list back together after a plugin was turned
// on or off, keeping the plugins already loaded.
func (s *Server) rebuildSources() {
	kept := make([]source, 0, len(s.sources))
	for _, p := range s.plugins {
		if s.pluginDisabled(p.Info().ID) {
			continue
		}
		if v := p.Video(); v != nil {
			kept = append(kept, source{id: v.Info().ID, name: v.Info().Name, src: v})
		}
	}
	kept = append(kept, source{id: local.ID, name: i18n.T("Local"), src: s.localSrc})
	s.sources = kept
	s.orderSources()
	if s.sourceOf(s.settings.Source) == nil && len(s.sources) > 0 {
		s.settings.Source = s.sources[0].id
	}
}

func (s *Server) handleLocalFolder(w http.ResponseWriter, r *http.Request) {
	var q struct {
		Path   string `json:"path"`
		Remove bool   `json:"remove"`
	}
	if err := json.NewDecoder(r.Body).Decode(&q); err != nil || strings.TrimSpace(q.Path) == "" {
		http.Error(w, `{"error":"bad path"}`, http.StatusBadRequest)
		return
	}
	path := filepath.Clean(q.Path)
	if !q.Remove {
		info, err := os.Stat(path)
		if err != nil || !info.IsDir() {
			http.Error(w, `{"error":"not a folder"}`, http.StatusBadRequest)
			return
		}
	}
		kept := make([]string, 0, len(s.settings.LocalFolders)+1)
		found := false
		for _, have := range s.settings.LocalFolders {
			if have == path {
				found = true
				if q.Remove {
					continue
				}
			}
			kept = append(kept, have)
		}
		folders := kept
	if !q.Remove && !found {
		folders = append(folders, path)
	}
	s.settings.LocalFolders = folders
	s.localSrc.Folders = append([]string(nil), folders...)
	s.settings.save()
	s.writeJSON(w, map[string]any{"folders": folders})
}

func (s *Server) handleFollow(w http.ResponseWriter, r *http.Request) {
	var q struct {
		Item video.Item `json:"item"`
	}
	if err := json.NewDecoder(r.Body).Decode(&q); err != nil || q.Item.ID == "" {
		http.Error(w, `{"error":"bad item"}`, http.StatusBadRequest)
		return
	}
	on := s.lib.ToggleFollow(q.Item)
	s.writeJSON(w, map[string]any{"followed": on})
}

// ---- the small things ----

func (s *Server) writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.Encode(v)
}

// videoProblem says what went wrong with a source's ask, for an API error:
// the source's own reason, when it gave one worth passing on.
func videoProblem(err error, msg string) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, video.ErrSignIn), errors.Is(err, video.ErrSetup),
		errors.Is(err, video.ErrUnavailable), errors.Is(err, video.ErrUnsupported):
		if reason := video.Reason(err); reason != "" {
			return reason
		}
		return msg
	}
	return msg + ": " + err.Error()
}

func atoiDefault(s string, def int) int {
	if s == "" {
		return def
	}
	if n, err := strconv.Atoi(s); err == nil && n > 0 {
		return n
	}
	return def
}

// randomToken is a cookie or session id worth its keep.
func randomToken() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// searchDeadline is how long a search waits for every source before it
// answers with whoever did.
var searchDeadline = 12 * time.Second

// sessionTTL is how long a login lasts.
const sessionTTL = 7 * 24 * time.Hour
