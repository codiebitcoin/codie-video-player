package app

import (
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
