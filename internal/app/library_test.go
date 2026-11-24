package app

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"codie-video-player/internal/library"
	"codie-video-player/internal/video"
)

func testLibrary(t *testing.T) *library.Library {
	t.Helper()
	dir, err := os.MkdirTemp("", "cvp-lib-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return library.Load(dir)
}

func item(id string) video.Item {
	return video.Item{ID: id, Title: "剧" + id, Source: "huangguo"}
}

func TestWatchRecordsAndResumes(t *testing.T) {
	// The history remembers what played, which episode, and how far into it,
	// and the same drama watched again moves up and keeps its place.
	l := testLibrary(t)
	l.Watch(item("a"), video.Episode{ID: "e1"}, 0, 0, 0, false)
	if len(l.History) != 1 || l.History[0].ID != "a" {
		t.Fatalf("watch didn't record: %+v", l.History)
	}
	l.Watch(item("b"), video.Episode{ID: "e1"}, 0, 0, 0, false)
	l.Watch(item("a"), video.Episode{ID: "e2"}, 1, 120, 600, false)
	if l.History[0].ID != "a" || l.History[0].EpID != "e2" || l.History[0].Pos != 120 {
		t.Fatalf("rewatch didn't move up with its place: %+v", l.History)
	}
	p := l.ProgressOf("a")
	if p == nil || p.EpID != "e2" || p.Pos != 120 {
		t.Fatalf("progressOf lost the place: %+v", p)
	}
}

func TestWatchCapsTheHistory(t *testing.T) {
	// The history keeps at most historyCap dramas, dropping the oldest.
	id := func(i int) string {
		return item(string(rune('a'+i%26)) + string(rune('a'+i/26))).ID
	}
	l := testLibrary(t)
	for i := 0; i < library.HistoryCap+10; i++ {
		l.Watch(item(id(i)), video.Episode{ID: "e"}, 0, 0, 0, false)
	}
	if len(l.History) != library.HistoryCap {
		t.Fatalf("history wasn't capped: %d", len(l.History))
	}
	if l.ProgressOf(id(0)) != nil {
		t.Fatalf("the oldest didn't drop out of the capped history")
	}
	if l.ProgressOf(id(15)) == nil {
		t.Fatalf("one within the cap went away")
	}
}

func TestSetProgressMarksDone(t *testing.T) {
	// A drama played to its end is done, and stays done in the library.
	l := testLibrary(t)
	l.Watch(item("a"), video.Episode{ID: "e1"}, 0, 0, 0, false)
	l.SetProgress("a", 599, 600, true)
	if p := l.ProgressOf("a"); p == nil || !p.Done {
		t.Fatalf("setProgress didn't mark done: %+v", l.ProgressOf("a"))
	}
}

func TestToggleFollow(t *testing.T) {
	// Following a drama keeps it; following again lets it go. A follow
	// carries how far the drama was.
	l := testLibrary(t)
	l.Watch(item("a"), video.Episode{ID: "e2"}, 1, 100, 500, false)
	if !l.ToggleFollow(item("a")) {
		t.Fatalf("first follow didn't follow")
	}
	if !l.Followed("a") {
		t.Fatalf("followed lost it")
	}
	if l.Follows[0].Pos != 100 || l.Follows[0].EpID != "e2" {
		t.Fatalf("follow didn't carry the place: %+v", l.Follows[0])
	}
	if l.ToggleFollow(item("a")) {
		t.Fatalf("second follow didn't unfollow")
	}
	if l.Followed("a") {
		t.Fatalf("unfollow left it")
	}
}

func TestLibraryPersists(t *testing.T) {
	// What the library keeps survives going away and coming back.
	dir, err := os.MkdirTemp("", "cvp-lib-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	l := library.Load(dir)
	l.Watch(item("a"), video.Episode{ID: "e1"}, 0, 42, 300, false)
	l.ToggleFollow(item("a"))
	l.Save()
	again := library.Load(dir)
	if p := again.ProgressOf("a"); p == nil || p.Pos != 42 {
		t.Fatalf("the library didn't come back: %+v", again.ProgressOf("a"))
	}
	if !again.Followed("a") {
		t.Fatalf("the follows didn't come back")
	}
}

func TestLibraryIsPerSource(t *testing.T) {
	// What was followed or watched on one source doesn't show on another.
	// An entry with no source recorded (an older file) shows on every one.
	l := testLibrary(t)
	l.Watch(video.Item{ID: "y", Title: "yt", Source: "youtube"}, video.Episode{ID: "e"}, 0, 0, 0, false)
	l.Watch(video.Item{ID: "b", Title: "bili", Source: "bilibili"}, video.Episode{ID: "e"}, 0, 0, 0, false)
	l.ToggleFollow(video.Item{ID: "y", Title: "yt", Source: "youtube"})
	follows, history := l.Of("youtube")
	if len(follows) != 1 || follows[0].ID != "y" {
		t.Fatalf("youtube follows: %+v", follows)
	}
	if len(history) != 1 || history[0].ID != "y" {
		t.Fatalf("youtube history: %+v", history)
	}
	if _, history = l.Of("bilibili"); len(history) != 1 || history[0].ID != "b" {
		t.Fatalf("bilibili history: %+v", history)
	}
	l.History = append(l.History, library.Watched{Item: video.Item{ID: "old", Title: "旧的"}})
	if _, history = l.Of("youtube"); len(history) != 2 {
		t.Fatalf("an entry with no source should show everywhere: %+v", history)
	}
}

func TestForget(t *testing.T) {
	// Forgetting takes a drama out of both the history and the follows.
	l := testLibrary(t)
	l.Watch(item("a"), video.Episode{ID: "e"}, 0, 0, 0, false)
	l.ToggleFollow(item("a"))
	l.Forget("a")
	if l.ProgressOf("a") != nil || l.Followed("a") {
		t.Fatalf("forget left something: %+v", l.History)
	}
}

func TestShelfCaption(t *testing.T) {
	// A shelf card says which episode is on, or that it came to its end.
	tests := []struct {
		e    library.Watched
		want string
	}{
		{library.Watched{EpAt: 2, Ep: "大结局"}, "Ep 3 · 大结局"},
		{library.Watched{EpAt: 0, Ep: ""}, "Ep 1"},
		{library.Watched{EpAt: 5, Done: true}, "Finished"},
	}
	for _, tt := range tests {
		if got := shelfCaption(tt.e); got != tt.want {
			t.Errorf("shelfCaption(%+v) = %q, want %q", tt.e, got, tt.want)
		}
	}
}

func TestCardBadge(t *testing.T) {
	// A cover's badge names the episode, with the percent when it's known.
	if got := cardBadge(&library.Watched{EpAt: 3}); got != "Ep 4" {
		t.Errorf("cardBadge = %q", got)
	}
	if got := cardBadge(&library.Watched{EpAt: 3, Pos: 90, Dur: 300}); got != "Ep 4 30%" {
		t.Errorf("cardBadge with dur = %q", got)
	}
}

func TestWatchedStamp(t *testing.T) {
	// Every watch carries its moment, newest first in the history.
	l := testLibrary(t)
	l.Watch(item("a"), video.Episode{ID: "e"}, 0, 0, 0, false)
	time.Sleep(2 * time.Millisecond)
	l.Watch(item("b"), video.Episode{ID: "e"}, 0, 0, 0, false)
	if !l.History[0].When.After(l.History[1].When) {
		t.Fatalf("the newest watch wasn't stamped latest")
	}
	_ = filepath.Join
}
