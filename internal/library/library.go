// Package library is what the viewer keeps: how far each drama is, what they
// watched lately, and what they follow. It lives in one JSON file, loaded
// once at start and rewritten on change. The desktop app touches it on the
// main thread; the web server guards it with the mutex, so every read and
// write here locks.
//
// What the viewer keeps is per source: a drama followed on Bilibili is not a
// drama followed on YouTube. An entry whose source was never written (an
// older file) belongs to every source, so nothing already watched goes away.
package library

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"codie-video-player/internal/video"
)

// Watched is one drama as the library keeps it: what it is, which episode
// was last on, how far into it, and when that was.
type Watched struct {
	video.Item
	EpID string    `json:"epId,omitempty"`
	EpAt int       `json:"epAt,omitempty"`
	Ep   string    `json:"ep,omitempty"`
	Pos  float64   `json:"pos,omitempty"` // seconds into the episode
	Dur  float64   `json:"dur,omitempty"` // the episode's length, when known
	Done bool      `json:"done,omitempty"`
	When time.Time `json:"when"`
}

// HistoryCap is the most dramas the history keeps.
const HistoryCap = 60

// Library is the viewer's own: follows first, then the history.
type Library struct {
	mu      sync.Mutex
	path    string
	Follows []Watched `json:"follows,omitempty"`
	History []Watched `json:"history,omitempty"`
}

// Load reads the library from the folder the app keeps its data in, or
// starts an empty one.
func Load(dir string) *Library {
	l := &Library{path: filepath.Join(dir, "library.json")}
	l.load()
	return l
}

// load reads the file, without the lock (the library isn't shared yet).
func (l *Library) load() {
	if b, err := os.ReadFile(l.path); err == nil {
		json.Unmarshal(b, l)
	}
}

// Save writes the library out.
func (l *Library) Save() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.save()
}

// save writes the library out; the lock is held.
func (l *Library) save() {
	if l.path == "" {
		return
	}
	os.MkdirAll(filepath.Dir(l.path), 0o700)
	if b, err := json.MarshalIndent(l, "", "  "); err == nil {
		os.WriteFile(l.path, b, 0o600)
	}
}

// ProgressOf is what the library keeps of a drama, or nil. The pointer
// points into the library; take the lock's word that it shifts only under
// another call, and copy it before holding on to it.
func (l *Library) ProgressOf(id string) *Watched {
	l.mu.Lock()
	defer l.mu.Unlock()
	for i := range l.History {
		if l.History[i].ID == id {
			w := l.History[i]
			return &w
		}
	}
	return nil
}

// Followed reports whether a drama is in the follows.
func (l *Library) Followed(id string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, f := range l.Follows {
		if f.ID == id {
			return true
		}
	}
	return false
}

// ToggleFollow follows a drama, or stops following it. It says which of the
// two came to be.
func (l *Library) ToggleFollow(item video.Item) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
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
	l.Follows = append([]Watched{{Item: item, When: time.Now()}}, l.Follows...)
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

// Watch records that an episode of a drama is on: it moves to the front of
// the history, carrying how far into it the viewer is.
func (l *Library) Watch(item video.Item, ep video.Episode, at int, pos, dur float64, done bool) {
	if item.ID == "" {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
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
			l.History = append([]Watched{e}, l.History[:i]...)
			l.trimSave()
			return
		}
	}
	l.History = append([]Watched{{Item: item, EpID: ep.ID, EpAt: at, Ep: ep.Title,
		Pos: pos, Dur: dur, Done: done, When: now}}, l.History...)
	l.trimSave()
}

// SetProgress rewrites how far into a drama the viewer is, keeping its place
// in the history. A pos of exactly 0 starts the episode over.
func (l *Library) SetProgress(id string, pos, dur float64, done bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for i := range l.History {
		if l.History[i].ID == id {
			l.History[i].Pos, l.History[i].Dur, l.History[i].Done = pos, dur, done
			l.History[i].When = time.Now()
			l.trimSave()
			return
		}
	}
}

// Snapshot is the follows and the history, copied, for handing to something
// that reads them without the library's lock (a JSON response).
func (l *Library) Snapshot() (follows, history []Watched) {
	l.mu.Lock()
	defer l.mu.Unlock()
	follows = append([]Watched(nil), l.Follows...)
	history = append([]Watched(nil), l.History...)
	return follows, history
}

// Of is the follows and the history of one source, newest first as they are
// kept. An entry with no source recorded belongs to every source.
func (l *Library) Of(source string) (follows, history []Watched) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return ofSource(l.Follows, source), ofSource(l.History, source)
}

// ofSource keeps the entries of one source, in the order they were kept.
func ofSource(entries []Watched, source string) []Watched {
	out := make([]Watched, 0, len(entries))
	for _, e := range entries {
		if e.Source == "" || e.Source == source {
			out = append(out, e)
		}
	}
	return out
}

// trimSave keeps the history within its cap and writes the library out; the
// lock is held.
func (l *Library) trimSave() {
	if len(l.History) > HistoryCap {
		l.History = l.History[:HistoryCap]
	}
	l.save()
}

// Forget drops a drama from both the history and the follows.
func (l *Library) Forget(id string) {
	l.mu.Lock()
	defer l.mu.Unlock()
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

// progressOf is what the library keeps of a drama, or nil; the lock is held.
func (l *Library) progressOf(id string) *Watched {
	for i := range l.History {
		if l.History[i].ID == id {
			return &l.History[i]
		}
	}
	return nil
}
