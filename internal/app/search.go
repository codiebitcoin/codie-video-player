package app

import (
	"log"
	"strings"
	"time"

	"codie-video-player/internal/i18n"
	"codie-video-player/internal/video"
)

// A search asks every source that searches at once, and the answers come
// back one group to a source, so the grid can say which source had what.
// The last query's words stay in the settings, as chips under the field.

// searchGroup is what one source answered, or why it didn't.
type searchGroup struct {
	id    string
	name  string
	items []video.Item
	err   string
}

// searchAll asks the sources what q is. The groups land one to one, each
// stamped with its source, and the first whole group clears the loading.
func (a *app) searchAll(q string) {
	q = strings.TrimSpace(q)
	if q == "" || a.sources == nil {
		return
	}
	a.searching = true
	a.searchFilter = "all"
	a.searchGroups = nil
	a.gridErr = ""
	a.gridLoading = true
	a.clearGrid()
	a.rememberSearch(q)
	asked := 0
	for i := range a.sources {
		e := &a.sources[i]
		if !e.src.Can("videoSearch") {
			continue
		}
		asked++
		go func() {
			r, err := e.src.Search(q, 0)
			a.post(func() {
				// The search may have moved on before an answer came.
				if !a.searching || a.query != q {
					return
				}
				a.gridLoading = false
				g := searchGroup{id: e.id, name: e.name}
				if err != nil {
					log.Printf("search %q in %s: %v", q, e.id, err)
					g.err = videoProblem(err, i18n.T("Search failed"))
				} else {
					g.items = r.Items
					for j := range g.items {
						if g.items[j].Source == "" {
							g.items[j].Source = e.id
						}
					}
				}
				a.searchGroups = append(a.searchGroups, g)
			})
		}()
	}
	if asked == 0 {
		a.post(func() {
			a.gridLoading = false
			a.gridErr = i18n.T("None of the sources can search")
		})
	}
}

// searchItems is the items the grid shows of a search: the chosen source's,
// or every source's together.
func (a *app) searchItems() []video.Item {
	var items []video.Item
	for _, g := range a.searchGroups {
		if a.searchFilter != "all" && a.searchFilter != g.id {
			continue
		}
		items = append(items, g.items...)
	}
	return items
}

// searchProblems is what went wrong in a search, one line to a source.
func (a *app) searchProblems() string {
	var lines []string
	for _, g := range a.searchGroups {
		if g.err != "" {
			lines = append(lines, g.name+": "+g.err)
		}
	}
	return strings.Join(lines, "\n")
}

// rememberSearch keeps a query among the search's last words, newest first,
// without repeats, and saves the settings soon.
func (a *app) rememberSearch(q string) {
	for i, s := range a.settings.SearchHistory {
		if s == q {
			a.settings.SearchHistory = append(a.settings.SearchHistory[:i],
				a.settings.SearchHistory[i+1:]...)
			break
		}
	}
	a.settings.SearchHistory = append([]string{q}, a.settings.SearchHistory...)
	if len(a.settings.SearchHistory) > 10 {
		a.settings.SearchHistory = a.settings.SearchHistory[:10]
	}
	a.saveSettingsSoon()
}

// suggestDelay is how long the search field waits before it stops showing
// the last queries, once its words change.
const suggestDelay = 150 * time.Millisecond

// allPlaylists returns the playlist tabs the showing source knows (favorites
// folders, saved collections, watch later).
func (a *app) allPlaylists() []video.Tab {
	var out []video.Tab
	for _, t := range a.tabs {
		if isPlaylistTab(t.ID) {
			out = append(out, t)
		}
	}
	return out
}

// matchingPlaylists returns the playlist tabs whose names match q. A generic
// playlist word ("播放列表", "收藏", "playlist") matches all of them.
func (a *app) matchingPlaylists(q string) []video.Tab {
	all := a.allPlaylists()
	needle := strings.ToLower(strings.TrimSpace(q))
	if needle == "" {
		return all
	}
	for _, kw := range []string{"播放列表", "收藏", "合集", "playlist", "favorite", "プレイリスト"} {
		if strings.Contains(needle, kw) {
			return all
		}
	}
	var out []video.Tab
	for _, t := range all {
		if strings.Contains(strings.ToLower(t.Label), needle) {
			out = append(out, t)
		}
	}
	return out
}

