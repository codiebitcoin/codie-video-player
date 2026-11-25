package app

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"codie-video-player/internal/codieout"
	"codie-video-player/internal/library"
	"codie-video-player/internal/video"
)

// fakeToolSource answers a search the way a plugin does, for the tools to
// pick a drama from.
type fakeToolSource struct {
	id, name  string
	items     []video.Item
	listItems []video.Item // what List answers on page 1, for recommendations
	eps       int
}

func (f *fakeToolSource) Info() video.Info     { return video.Info{ID: f.id, Name: f.name} }
func (f *fakeToolSource) Can(what string) bool { return what == "videoSearch" || what == "videoList" }
func (f *fakeToolSource) Tabs() ([]video.Tab, error) {
	return []video.Tab{{ID: "hot", Label: "热门"}}, nil
}
func (f *fakeToolSource) List(tab string, page int) (*video.List, error) {
	if page == 1 {
		return &video.List{Items: f.listItems}, nil
	}
	return nil, nil
}
func (f *fakeToolSource) Search(string, int) (*video.VideoResult, error) {
	return &video.VideoResult{Items: f.items}, nil
}

func (f *fakeToolSource) Video(id string) (*video.Video, error) {
	d := &video.Video{Item: video.Item{ID: id, Title: "测试剧"}}
	for i := 0; i < f.eps; i++ {
		d.Episodes = append(d.Episodes, video.Episode{ID: fmt.Sprintf("ep%d", i+1), Title: fmt.Sprintf("第%d集", i+1)})
	}
	return d, nil
}

func (f *fakeToolSource) Play(id, ep string) (*video.Stream, error) {
	return &video.Stream{URL: "http://x/y.mp4", Type: "mp4"}, nil
}

// toolApp is an app with one fake source and the state the tools' handlers
// touch all inline: nothing crosses to a loop, and no file ever makes.
func toolApp(t *testing.T) *app {
	t.Helper()
	oldRead, oldMake, oldOpen, oldWait := readOnMain, findFFmpegFn, openEpisodeStream, toolSearchWait
	readOnMain = func(fn func()) { fn() }
	findFFmpegFn = func() string { return "" }
	openEpisodeStream = func(*video.Stream, float64) (*ui.VideoPlayer, string, bool, error) {
		return nil, "", false, fmt.Errorf("no player in tests")
	}
	toolSearchWait = 300 * time.Millisecond
	t.Cleanup(func() {
		readOnMain, findFFmpegFn, openEpisodeStream, toolSearchWait = oldRead, oldMake, oldOpen, oldWait
	})
	a := &app{settings: &settings{}, rate: 1, lib: &library.Library{}}
	a.sources = []sourceEntry{{id: "t", name: "测试源", src: &fakeToolSource{
		id: "t", name: "测试源", eps: 2, items: []video.Item{{ID: "d1", Title: "测试剧"}},
	}}}
	a.sourceEntry = &a.sources[0]
	return a
}

// toolHandle runs one tool's Handle with args and answers what it said.
func toolHandle(t *testing.T, h func(ctx context.Context, args json.RawMessage) (any, error), args string) (any, error) {
	t.Helper()
	return h(context.Background(), json.RawMessage(args))
}

func TestAgentCatalogShapes(t *testing.T) {
	// Every tool the device sees is named as the firmware takes, says what
	// it does, and keeps its schemas and timeout inside the device's rules.
	a := toolApp(t)
	for _, tl := range a.agentToolCatalog() {
		if !regexp.MustCompile(`^[a-z][a-z0-9_]{0,23}$`).MatchString(tl.Name) {
			t.Errorf("tool %q isn't a name the device takes", tl.Name)
		}
		if tl.Description == "" {
			t.Errorf("tool %s says nothing of itself", tl.Name)
		}
		if tl.TimeoutMS < 100 || tl.TimeoutMS > 20000 {
			t.Errorf("tool %s timeout %d is outside the device's range", tl.Name, tl.TimeoutMS)
		}
		for _, label := range []struct {
			what string
			b    []byte
		}{{"parameters", tl.Parameters}, {"result_schema", tl.ResultSchema}} {
			if len(label.b) == 0 {
				t.Errorf("tool %s has no %s", tl.Name, label.what)
				continue
			}
			var m map[string]any
			if err := json.Unmarshal(label.b, &m); err != nil {
				t.Fatalf("tool %s %s: %v", tl.Name, label.what, err)
			}
			if m["type"] != "object" || m["additionalProperties"] != false {
				t.Errorf("tool %s %s isn't a closed object schema", tl.Name, label.what)
			}
			if _, ok := m["properties"].(map[string]any); !ok {
				t.Errorf("tool %s %s names no properties", tl.Name, label.what)
			}
		}
	}
}

func TestWhatsPlayingTool(t *testing.T) {
	// whats_playing says what's on, or owns up that nothing is.
	a := toolApp(t)
	h := a.agentToolCatalog()[0].Handle
	if _, err := toolHandle(t, h, "{}"); err == nil {
		t.Error("nothing playing, but the tool said something was")
	}
	a.playing = playing{item: video.Item{ID: "d1", Title: "测试剧"}, ep: video.Episode{ID: "ep2", Title: "第2集"}, at: 1}
	a.detail = &video.Video{Item: video.Item{ID: "d1"}, Episodes: make([]video.Episode, 8)}
	out, err := toolHandle(t, h, "{}")
	if err != nil {
		t.Fatalf("whats_playing: %v", err)
	}
	st := out.(playState)
	if st.Title != "测试剧" || st.Episode != "第2集" || st.EpNumber != 2 || st.EpTotal != 8 {
		t.Errorf("whats_playing = %+v", st)
	}
}

func TestPauseResumeTools(t *testing.T) {
	// pause and resume reach the device's session when the picture is
	// there, and own up when nothing plays.
	a := toolApp(t)
	a.deviceOutput, a.codie = true, codieout.New(&codieout.Creds{Addr: "127.0.0.1:1"})
	var got []string
	old := codieMediaAction
	codieMediaAction = func(c *codieout.Client, session, action string, args map[string]any) {
		got = append(got, action)
	}
	t.Cleanup(func() { codieMediaAction = old })

	catalog := a.agentToolCatalog()
	var pause, resume func(context.Context, json.RawMessage) (any, error)
	for _, tl := range catalog {
		switch tl.Name {
		case "pause":
			pause = tl.Handle
		case "resume":
			resume = tl.Handle
		}
	}
	if _, err := toolHandle(t, pause, "{}"); err == nil {
		t.Error("no session, but pause said it paused")
	}
	a.codieSession = "s1"
	if _, err := toolHandle(t, pause, "{}"); err != nil {
		t.Fatalf("pause: %v", err)
	}
	if _, err := toolHandle(t, resume, "{}"); err != nil {
		t.Fatalf("resume: %v", err)
	}
	if len(got) != 2 || got[0] != "pause" || got[1] != "resume" {
		t.Errorf("device got %v, want [pause resume]", got)
	}
}

func TestSetSpeedTool(t *testing.T) {
	// set_speed moves the rate the app plays at, and turns its face from a
	// speed the menu doesn't have.
	a := toolApp(t)
	var setSpeed func(context.Context, json.RawMessage) (any, error)
	for _, tl := range a.agentToolCatalog() {
		if tl.Name == "set_speed" {
			setSpeed = tl.Handle
		}
	}
	if _, err := toolHandle(t, setSpeed, `{"rate":1.3}`); err == nil {
		t.Error("a speed off the menu went through")
	}
	if _, err := toolHandle(t, setSpeed, `{"rate":1.5}`); err != nil {
		t.Fatalf("set_speed 1.5: %v", err)
	}
	if a.rate != 1.5 {
		t.Errorf("rate = %v, want 1.5", a.rate)
	}
}

func TestStepEpisodeTool(t *testing.T) {
	// next and previous move the episode, and say so when there is none.
	a := toolApp(t)
	var next, prev func(context.Context, json.RawMessage) (any, error)
	for _, tl := range a.agentToolCatalog() {
		switch tl.Name {
		case "next_episode":
			next = tl.Handle
		case "previous_episode":
			prev = tl.Handle
		}
	}
	if _, err := toolHandle(t, next, "{}"); err == nil {
		t.Error("nothing playing, but next said it moved")
	}
	a.playing = playing{item: video.Item{ID: "d1", Title: "测试剧"}, ep: video.Episode{ID: "ep1"}, at: 0}
	a.detail = &video.Video{Item: video.Item{ID: "d1"}, Episodes: []video.Episode{
		{ID: "ep1", Title: "第1集"}, {ID: "ep2", Title: "第2集"},
	}}
	out, err := toolHandle(t, next, "{}")
	if err != nil {
		t.Fatalf("next: %v", err)
	}
	if a.playing.ep.ID != "ep2" {
		t.Errorf("next landed on %q, want ep2", a.playing.ep.ID)
	}
	if out.(map[string]any)["episode"] != "第2集" {
		t.Errorf("next said %v", out)
	}
	if _, err := toolHandle(t, prev, "{}"); err != nil {
		t.Fatalf("prev: %v", err)
	}
	if a.playing.ep.ID != "ep1" {
		t.Errorf("prev landed on %q, want ep1", a.playing.ep.ID)
	}
	if _, err := toolHandle(t, prev, "{}"); err == nil {
		t.Error("before the first episode, prev said it moved")
	}
}

func TestSearchPlayTool(t *testing.T) {
	// search_play takes the first drama a source answers and starts its
	// first episode; a search with no answers says not found.
	a := toolApp(t)
	var searchPlay func(context.Context, json.RawMessage) (any, error)
	for _, tl := range a.agentToolCatalog() {
		if tl.Name == "search_play" {
			searchPlay = tl.Handle
		}
	}
	out, err := toolHandle(t, searchPlay, `{"query":"测试"}`)
	if err != nil {
		t.Fatalf("search_play: %v", err)
	}
	got := out.(map[string]any)
	if got["found"] != true || got["title"] != "测试剧" || got["source"] != "测试源" {
		t.Errorf("search_play = %v", got)
	}
	if a.playing.item.ID != "d1" || a.playing.ep.ID != "ep1" {
		t.Errorf("search_play started %+v / %q, want d1 / ep1", a.playing.item, a.playing.ep.ID)
	}

	empty := toolApp(t)
	empty.sources[0].src = &fakeToolSource{id: "t", name: "测试源"}
	empty.sourceEntry = &empty.sources[0]
	for _, tl := range empty.agentToolCatalog() {
		if tl.Name == "search_play" {
			out, err := toolHandle(t, tl.Handle, `{"query":"没有的剧"}`)
			if err != nil {
				t.Fatalf("search_play empty: %v", err)
			}
			if out.(map[string]any)["found"] != false {
				t.Errorf("empty search said %v", out)
			}
		}
	}
	if _, err := toolHandle(t, searchPlay, `{"query":"  "}`); err == nil {
		t.Error("a blank query went through")
	}
}

func TestToolsRefreshLifecycle(t *testing.T) {
	// The tools come only when asked for and a device is paired, and go
	// when either goes.
	a := toolApp(t)
	a.settings.ConversationControl = true
	a.toolsRefresh()
	if a.tools != nil {
		t.Error("no device paired, but the tools came up")
	}
	a.codie = codieout.New(&codieout.Creds{Addr: "127.0.0.1:1"})
	a.toolsRefresh()
	if a.tools == nil {
		t.Error("paired and asked for, but the tools stayed down")
	}
	a.settings.ConversationControl = false
	a.toolsRefresh()
	if a.tools != nil {
		t.Error("turned off, but the tools stayed up")
	}
}

func TestRecommendTool(t *testing.T) {
	// An unfinished follow is the pick: it goes on from where it was, and
	// says so.
	a := toolApp(t)
	a.lib.Follows = []library.Watched{{Item: video.Item{ID: "d1", Title: "追的剧"}, EpID: "ep2", EpAt: 1, Done: false}}
	out, err := toolHandle(t, func(ctx context.Context, args json.RawMessage) (any, error) { return a.toolsRecommend(ctx) }, `{}`)
	if err != nil {
		t.Fatal(err)
	}
	m := out.(map[string]any)
	if m["found"] != true || m["title"] != "追的剧" || m["reason"] != "追剧" {
		t.Fatalf("recommend of a follow = %+v", m)
	}
	if a.playing.item.ID != "d1" || a.playing.at != 1 {
		t.Fatalf("the follow didn't play from its own place: %+v", a.playing)
	}
}

func TestRecommendToolFallsToTheList(t *testing.T) {
	// Without a follow, the current page names a drama the viewer hasn't
	// started, and that one plays.
	a := toolApp(t)
	a.sources[0].src = &fakeToolSource{
		id: "t", name: "测试源", eps: 2,
		listItems: []video.Item{{ID: "d9", Title: "热门剧"}},
	}
	out, err := toolHandle(t, func(ctx context.Context, args json.RawMessage) (any, error) { return a.toolsRecommend(ctx) }, `{}`)
	if err != nil {
		t.Fatal(err)
	}
	m := out.(map[string]any)
	if m["found"] != true || m["title"] != "热门剧" || m["reason"] != "热门" {
		t.Fatalf("recommend of the page = %+v", m)
	}
}

func TestRecommendToolSkipsWatched(t *testing.T) {
	// A drama already in the history isn't recommended again; past it, the
	// page's next name is.
	a := toolApp(t)
	a.sources[0].src = &fakeToolSource{
		id: "t", name: "测试源", eps: 2,
		listItems: []video.Item{{ID: "d1", Title: "看过的"}, {ID: "d2", Title: "没看过的"}},
	}
	a.lib.History = []library.Watched{{Item: video.Item{ID: "d1", Title: "看过的"}, EpAt: 0}}
	out, err := toolHandle(t, func(ctx context.Context, args json.RawMessage) (any, error) { return a.toolsRecommend(ctx) }, `{}`)
	if err != nil {
		t.Fatal(err)
	}
	m := out.(map[string]any)
	if m["title"] != "没看过的" {
		t.Fatalf("recommend didn't skip the watched: %+v", m)
	}
}

func TestRecommendToolNothing(t *testing.T) {
	// With nothing followed and an empty page, it owns up plainly.
	a := toolApp(t)
	out, err := toolHandle(t, func(ctx context.Context, args json.RawMessage) (any, error) { return a.toolsRecommend(ctx) }, `{}`)
	if err != nil {
		t.Fatal(err)
	}
	if out.(map[string]any)["found"] != false {
		t.Fatalf("recommend of nothing = %+v", out)
	}
}
