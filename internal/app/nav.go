package app

import "codie-video-player/internal/video"

// Where the window has been, for its back and forward buttons. The stack is
// small on purpose: what forward remembers is a drama's page someone backed
// out of, which is the only page a back leaves behind.

// location is a page as it was: which one, of what, in what state.
type location struct {
	page      pageKind
	sourceID  string
	tabID     string
	query     string
	searching bool
	detailOf  string
}

// here is where the window stands now, as a location.
func (a *app) here() location {
	loc := location{page: a.page, sourceID: a.sourceName(), tabID: a.tabID,
		query: a.query, searching: a.searching}
	if a.page == pagePlay {
		loc.page = pageDetail // a play comes back as its drama's page
	}
	if a.detail != nil {
		loc.detailOf = a.detail.ID
	}
	return loc
}

// back goes back a page of the window, leaving where it was for forward.
func (a *app) back() {
	if a.page == pagePlay {
		a.cancelNext()
		a.recordPlayProgress()
		a.stopPlayer()
		a.drawerOpen, a.speedOpen = false, false
		a.playerErr = ""
		a.playing = playing{}
		if a.detailOf == "" {
			a.page = pageList
			a.detail = nil
		} else {
			a.page = pageDetail
		}
		return
	}
	if a.page == pageDetail {
		if a.detail != nil {
			a.fwd = append(a.fwd, a.here())
			if len(a.fwd) > 20 {
				a.fwd = a.fwd[1:]
			}
		}
		a.drawerOpen = false
		a.page = pageList
		a.detail = nil
		a.detailOf = ""
	}
}

// forward goes back to the page a back left, if there is one.
func (a *app) forward() {
	if len(a.fwd) == 0 {
		return
	}
	loc := a.fwd[len(a.fwd)-1]
	a.fwd = a.fwd[:len(a.fwd)-1]
	if loc.page != pageDetail || loc.detailOf == "" {
		return
	}
	// The drama's page opens again, from its own source.
	src := a.sourceOf(loc.sourceID)
	if src != nil && a.sourceEntry != nil && a.sourceEntry.id != src.id {
		a.useSource(src.id)
	}
	for _, it := range a.items {
		if it.ID == loc.detailOf {
			a.openDetail(it)
			return
		}
	}
	a.openDetail(video.Item{ID: loc.detailOf, Source: loc.sourceID})
}
