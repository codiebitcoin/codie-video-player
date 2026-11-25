package app

import (
	"fmt"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"codie-video-player/internal/library"
	"codie-video-player/internal/video"
)

// fakeSource is a source of videos that answers from memory: pages of items,
// each item one episode, so playback can be watched without a network.
type fakeSource struct {
	id    string
	pages [][]video.Item
}

func (f *fakeSource) Info() video.Info { return video.Info{ID: f.id, Name: f.id} }
func (f *fakeSource) Can(what string) bool {
	switch what {
	case "videoTabs", "videoList", "videoItem", "videoPlay":
		return true
	}
	return false
}
func (f *fakeSource) Tabs() ([]video.Tab, error) {
	return []video.Tab{{ID: "hot", Label: "Hot"}, {ID: "feed", Label: "Feed"}, {ID: "fav:1", Label: "Favs"}}, nil
}
func (f *fakeSource) List(string, int) (*video.List, error) { return nil, nil }
func (f *fakeSource) Search(string, int) (*video.VideoResult, error) {
	return nil, video.ErrUnsupported
}
func (f *fakeSource) Video(id string) (*video.Video, error) {
	return &video.Video{Item: video.Item{ID: id, Source: f.id}, Episodes: []video.Episode{{ID: "e1", Title: "Ep 1"}}}, nil
}
func (f *fakeSource) Play(string, string) (*video.Stream, error) {
	return &video.Stream{URL: "http://x/y.mp4", Type: "mp4"}, nil
}

// pagedSource answers a page of items per call, which is what a feed does.
type pagedSource struct {
	fakeSource
	calls int
}

func (p *pagedSource) List(_ string, page int) (*video.List, error) {
	p.calls++
	items := []video.Item{
		{ID: fmt.Sprintf("p%d-a", page), Title: "a", Source: p.id},
		{ID: fmt.Sprintf("p%d-b", page), Title: "b", Source: p.id},
	}
	return &video.List{Items: items, More: true}, nil
}

// playbackApp is an app with one source, a playlist's worth of behaviour and
// nothing that needs a window.
func playbackApp(t *testing.T, src video.VideoSource) *app {
	t.Helper()
	oldOpen, oldFind := openEpisodeStream, findFFmpegFn
	openEpisodeStream = func(*video.Stream, float64) (*ui.VideoPlayer, string, bool, error) { return nil, "", false, nil }
	findFFmpegFn = func() string { return "" }
	t.Cleanup(func() { openEpisodeStream, findFFmpegFn = oldOpen, oldFind })
	a := &app{settings: &settings{}, rate: 1, vol: 100, lib: &library.Library{}}
	a.sources = []sourceEntry{{id: "bili", name: "bili", src: src}}
	a.sourceEntry = &a.sources[0]
	return a
}

// waitFor polls until cond holds, for the work that goes out on a goroutine.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestSinglePlayLeavesNoQueue(t *testing.T) {
	// One video played from a list that isn't a playlist: nothing follows it,
	// so next does nothing.
	src := &pagedSource{fakeSource: fakeSource{id: "bili"}}
	a := playbackApp(t, src)
	src.fakeSource.id = "bili"
	a.tabID = "hot"
	items := []video.Item{{ID: "one", Title: "one", Source: "bili"}}
	a.items = items
	a.playItemFromGrid(items, 0)
	if len(a.queue) != 0 {
		t.Fatalf("queue = %d, a plain list shouldn't queue", len(a.queue))
	}
	a.nextEpisode(false)
	if a.playing.item.ID != "one" {
		t.Fatalf("playing = %q, next on a plain list should stay put", a.playing.item.ID)
	}
}

func TestPlaylistPlaysThrough(t *testing.T) {
	// A playlist tab queues what it lists, and next moves through it.
	src := &pagedSource{fakeSource: fakeSource{id: "bili"}}
	a := playbackApp(t, src)
	a.tabID = "fav:1"
	items := []video.Item{
		{ID: "a", Title: "a", Source: "bili"},
		{ID: "b", Title: "b", Source: "bili"},
		{ID: "c", Title: "c", Source: "bili"},
	}
	a.playPlaylist(items, 0, false)
	if len(a.queue) != 3 || a.queueAt != 0 {
		t.Fatalf("queue = %d at %d, want 3 at 0", len(a.queue), a.queueAt)
	}
	if a.playing.item.ID != "a" {
		t.Fatalf("playing = %q, want a", a.playing.item.ID)
	}
	a.nextEpisode(false)
	if a.queueAt != 1 || a.playing.item.ID != "b" {
		t.Fatalf("at %d playing %q, want 1 and b", a.queueAt, a.playing.item.ID)
	}
	a.nextEpisode(false)
	if a.queueAt != 2 || a.playing.item.ID != "c" {
		t.Fatalf("at %d playing %q, want 2 and c", a.queueAt, a.playing.item.ID)
	}
}

func TestFeedAutostartsAndKeepsGoing(t *testing.T) {
	// The feed tab starts playing as soon as its first page is in, and goes on
	// to the next page when that one runs out.
	src := &pagedSource{fakeSource: fakeSource{id: "bili"}}
	a := playbackApp(t, src)
	a.tabID = "feed"
	a.feedAutostart = true
	a.loadTab()
	waitFor(t, "the feed to start", func() bool { return a.playing.item.ID != "" })
	if a.playing.item.ID != "p1-a" {
		t.Fatalf("playing = %q, want the first of page 1", a.playing.item.ID)
	}
	if !a.feedMode {
		t.Fatal("feed mode wasn't on while a feed played")
	}
	a.nextEpisode(false)
	if a.playing.item.ID != "p1-b" {
		t.Fatalf("playing = %q, want the second of page 1", a.playing.item.ID)
	}
	// The page is spent: the next one is asked for, and plays on from it.
	a.nextEpisode(false)
	waitFor(t, "the next feed page", func() bool { return a.playing.item.ID == "p2-a" })
	if src.calls < 2 {
		t.Fatalf("list calls = %d, the feed should have asked for another page", src.calls)
	}
}
