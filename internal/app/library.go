package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"codie-video-player/internal/video"
)

// The library is what the app keeps of the viewer's: how far each drama is,
// what they watched lately, and what they follow. It lives in one JSON file
// in the user data folder, loaded once at start and rewritten on change.
// Everything here is read and written on the main thread.

// watched is one drama as the library keeps it: what it is, which episode
// was last on, how far into it, and when that was.
type watched struct {
	video.Item
	EpID string    `json:"epId,omitempty"`
	EpAt int       `json:"epAt,omitempty"`
	Ep   string    `json:"ep,omitempty"`
	Pos  float64   `json:"pos,omitempty"` // seconds into the episode
	Dur  float64   `json:"dur,omitempty"` // the episode's length, when known
	Done bool      `json:"done,omitempty"`
	When time.Time `json:"when"`
}

// historyCap is the most dramas the history keeps.
const historyCap = 60

// library is the viewer's own: follows first, then the history.
type library struct {
	path    string
	Follows []watched `json:"follows,omitempty"`
	History []watched `json:"history,omitempty"`
}

// loadLibrary reads the library from the folder the app keeps its data in,
// or starts an empty one.
func loadLibrary(dir string) *library {
	l := &library{path: filepath.Join(dir, "library.json")}
	if b, err := os.ReadFile(l.path); err == nil {
		json.Unmarshal(b, l)
	}
	return l
}

// save writes the library out.
func (l *library) save() {
	if l.path == "" {
		return
	}
	os.MkdirAll(filepath.Dir(l.path), 0o700)
	if b, err := json.MarshalIndent(l, "", "  "); err == nil {
		os.WriteFile(l.path, b, 0o600)
	}
}

// progressOf is what the library keeps of a drama, or nil.
func (l *library) progressOf(id string) *watched {
	for i := range l.History {
		if l.History[i].ID == id {
			return &l.History[i]
		}
	}
	return nil
}

// followed reports whether a drama is in the follows.
func (l *library) followed(id string) bool {
	for _, f := range l.Follows {
		if f.ID == id {
			return true
		}
	}
	return false
}

// toggleFollow follows a drama, or stops following it. It says which of the
// two came to be.
func (l *library) toggleFollow(item video.Item) bool {
	at := -1
	for i := range l.Follows {
		if l.Follows[i].ID == item.ID {
			at = i
			break
		}
	}
	if at >= 0 {
		l.Follows = append(l.Follows[:at], l.Follows[at+1:]...)
		l.save()
		return false
	}
	l.Follows = append([]watched{{Item: item, When: time.Now()}}, l.Follows...)
	if p := l.progressOf(item.ID); p != nil {
		l.Follows[0].EpID, l.Follows[0].EpAt, l.Follows[0].Ep, l.Follows[0].Pos, l.Follows[0].Done =
			p.EpID, p.EpAt, p.Ep, p.Pos, p.Done
	}
	if len(l.Follows) > 100 {
		l.Follows = l.Follows[:100]
	}
	l.save()
	return true
}

// watch records that an episode of a drama is on: it moves to the front of
// the history, carrying how far into it the viewer is.
func (l *library) watch(item video.Item, ep video.Episode, at int, pos, dur float64, done bool) {
	if item.ID == "" {
		return
	}
	now := time.Now()
	for i := range l.History {
		if l.History[i].ID == item.ID {
			e := l.History[i]
			e.EpID, e.EpAt, e.Ep = ep.ID, at, ep.Title
			if pos > 0 || done {
				e.Pos, e.Dur, e.Done = pos, dur, done
			}
			e.When = now
			if item.Title != "" {
				e.Item = item
			}
			l.History = append([]watched{e}, l.History[:i]...)
			l.trimSave()
			return
		}
	}
	l.History = append([]watched{{Item: item, EpID: ep.ID, EpAt: at, Ep: ep.Title,
		Pos: pos, Dur: dur, Done: done, When: now}}, l.History...)
	l.trimSave()
}

// setProgress rewrites how far into a drama the viewer is, keeping its place
// in the history. A pos of exactly 0 starts the episode over.
func (l *library) setProgress(id string, pos, dur float64, done bool) {
	for i := range l.History {
		if l.History[i].ID == id {
			l.History[i].Pos, l.History[i].Dur, l.History[i].Done = pos, dur, done
			l.History[i].When = time.Now()
			l.trimSave()
			return
		}
	}
}

// trimSave keeps the history within its cap and writes the library out.
func (l *library) trimSave() {
	if len(l.History) > historyCap {
		l.History = l.History[:historyCap]
	}
	l.save()
}

// forget drops a drama from both the history and the follows.
func (l *library) forget(id string) {
	for i := range l.History {
		if l.History[i].ID == id {
			l.History = append(l.History[:i], l.History[i+1:]...)
			break
		}
	}
	for i := range l.Follows {
		if l.Follows[i].ID == id {
			l.Follows = append(l.Follows[:i], l.Follows[i+1:]...)
			break
		}
	}
	l.save()
}
