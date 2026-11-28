package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"codie-video-player/internal/codieout"
	"codie-video-player/internal/library"
	"codie-video-player/internal/video"
)

func TestCodieFFmpegArgs(t *testing.T) {
	// The usual speed of a stream already in the device's shape is a plain
	// copy; any other pace bakes itself in, which no copy can do.
	s := &video.Stream{URL: "http://x/v.mp4", Type: "mp4"}
	if got := codieFFmpegArgs(s, 1, true, "out.mp4", 0); !hasPair(got, "-c", "copy") {
		t.Errorf("as-is at 1× = %v, want a copy", got)
	}
	if got := codieFFmpegArgs(s, 1.5, false, "out.mp4", 0); hasPair(got, "-c", "copy") {
		t.Errorf("1.5× = %v, a copy can't change its pace", got)
	} else if !hasPair(got, "-vf", "setpts=PTS/1.5,scale=1920:1080:force_original_aspect_ratio=decrease,pad=ceil(iw/2)*2:ceil(ih/2)*2") {
		t.Errorf("1.5× = %v, the picture isn't sped", got)
	} else if !hasPair(got, "-af", "atempo=1.5") {
		t.Errorf("1.5× = %v, the sound isn't sped", got)
	}
	if got := codieFFmpegArgs(s, 1, false, "out.mp4", 0); hasStr(got, "setpts") || hasStr(got, "atempo") {
		t.Errorf("1× transcode = %v, nothing should be sped", got)
	}
	// A stream with its sound apart maps the two inputs together.
	s2 := &video.Stream{URL: "http://x/v.m3u8", AudioURL: "http://x/a.mp3", Type: "hls"}
	got := codieFFmpegArgs(s2, 1, false, "out.mp4", 0)
	if !hasPair(got, "-map", "0:v:0") || !hasPair(got, "-map", "1:a:0?") {
		t.Errorf("apart streams = %v, want both inputs mapped", got)
	}
}

func hasPair(args []string, flag, value string) bool {
	for i, a := range args {
		if a == flag && i+1 < len(args) && args[i+1] == value {
			return true
		}
	}
	return false
}

func hasStr(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}

func TestCodieResumeStart(t *testing.T) {
	// A session starts where the story was, in the file's faster clock; a
	// story barely begun isn't worth resuming.
	tests := []struct {
		resumeS, rate float64
		want          int64
	}{
		{0, 1, 0},
		{5, 1, 0}, // 太短,从头
		{100, 1, 100000},
		{100, 2, 50000}, // 2× 的文件里,故事 100 秒在文件 50 秒处
		{90, 0.5, 180000},
	}
	for _, tt := range tests {
		if got := codieResumeStart(tt.resumeS, tt.rate); got != tt.want {
			t.Errorf("codieResumeStart(%v, %v) = %d, want %d", tt.resumeS, tt.rate, got, tt.want)
		}
	}
}

func TestCodieEndedStartsNext(t *testing.T) {
	// When the device's episode plays out, the next one counts in, the way
	// the local player's own end does; a pause before the end doesn't.
	a := toolApp(t)
	a.playing = playing{item: video.Item{ID: "d1"}, ep: video.Episode{ID: "ep1"}, at: 0}
	a.detail = &video.Video{Item: video.Item{ID: "d1"}, Episodes: []video.Episode{
		{ID: "ep1"}, {ID: "ep2"},
	}}
	t.Cleanup(a.cancelNext)

	a.codieState = &codieout.MediaSessionState{IsPlaying: true, PositionMS: 119000, DurationMS: 120000}
	a.codieEnded()
	if a.nextLatch || a.nextLeft != 0 {
		t.Error("still playing, but the countdown came")
	}
	a.codieState = &codieout.MediaSessionState{IsPlaying: false, PositionMS: 30000, DurationMS: 120000}
	a.codieEnded()
	if a.nextLatch || a.nextLeft != 0 {
		t.Error("paused halfway, but the countdown came")
	}
	a.codieState = &codieout.MediaSessionState{IsPlaying: false, PositionMS: 119500, DurationMS: 120000}
	a.codieEnded()
	if !a.nextLatch || a.nextLeft != 8 {
		t.Errorf("played out: latch=%v left=%d, want the countdown at 8", a.nextLatch, a.nextLeft)
	}
	a.codieEnded() // 已 latch,不再重来
	if a.nextLeft != 8 || a.nextTimer == nil {
		t.Error("the countdown restarted")
	}
}

func TestDeviceShortcuts(t *testing.T) {
	// The keys answer the device's session when the picture is there:
	// space pause/resume, arrows seek and volume.
	a := toolApp(t)
	a.deviceOutput, a.codie = true, codieout.New(&codieout.Creds{Addr: "127.0.0.1:1"})
	a.codieSession = "s1"
	a.codieState = &codieout.MediaSessionState{IsPlaying: false}
	a.vol = 40
	var got []string
	old := codieMediaAction
	codieMediaAction = func(c *codieout.Client, session, action string, args map[string]any) {
		got = append(got, action+":"+fmt.Sprint(args["position_ms"], args["volume"]))
	}
	t.Cleanup(func() { codieMediaAction = old })

	a.playPause()
	a.seekBy(5 * time.Second)
	a.seekBy(-10 * time.Second)
	a.bumpVolume(5)
	a.bumpVolume(-50) // 到 0 为止
	joined := strings.Join(got, " ")
	if !strings.Contains(joined, "resume") {
		t.Errorf("space on a paused session = %q, want resume", joined)
	}
	if !strings.Contains(got[1], "5000") || !strings.Contains(got[2], "0") {
		t.Errorf("arrow seeks = %q, want +5s then clamped to 0", joined)
	}
	if !strings.Contains(got[3], "0.45") || !strings.Contains(got[4], "0") {
		t.Errorf("arrow volumes = %q, want 45 then floored at 0", joined)
	}
	if a.vol != 0 {
		t.Errorf("vol = %v, want 0 after the floor", a.vol)
	}
}

func TestRecordProgressInStoryTime(t *testing.T) {
	// What's kept of a sped-up episode is story time, not the file's
	// faster clock: at 2×, half the file is the whole story so far.
	a := toolApp(t)
	a.lib = &library.Library{}
	a.deviceOutput = true
	a.rate = 2
	a.playing = playing{item: video.Item{ID: "d1", Title: "测试剧"}, ep: video.Episode{ID: "ep1", Title: "第1集"}, at: 0}
	a.codieState = &codieout.MediaSessionState{IsPlaying: true, PositionMS: 60_000, DurationMS: 120_000}
	a.recordPlayProgress()
	if len(a.lib.History) != 1 {
		t.Fatalf("nothing recorded: %d entries", len(a.lib.History))
	}
	if got := a.lib.History[0].Pos; got != 120 {
		t.Errorf("pos = %v of the story, want 120", got)
	}
	if got := a.lib.History[0].Dur; got != 240 {
		t.Errorf("dur = %v of the story, want 240", got)
	}
}

func TestOutputToggleClicksTarget(t *testing.T) {
	// Clicking "codie" while on Local switches to the device, and clicking
	// "Local" while on the device switches back.
	a := toolApp(t)
	a.codie = codieout.New(&codieout.Creds{Addr: "127.0.0.1:1"})
	a.deviceOutput = false
	tester := ui.NewTester(func(c *ui.Context) {
		a.outputToggle(c)
	}, 400, 80)
	if err := tester.Click("codie"); err != nil {
		t.Fatalf("Click(codie): %v", err)
	}
	if !a.deviceOutput {
		t.Fatal("deviceOutput remained false after clicking codie")
	}
	if err := tester.Click("Local"); err != nil {
		t.Fatalf("Click(Local): %v", err)
	}
	if a.deviceOutput {
		t.Fatal("deviceOutput remained true after clicking Local")
	}
}

func TestSidebarClickFromDetailReturnsToList(t *testing.T) {
	a := toolApp(t)
	a.favOpen = true
	a.tabs = []video.Tab{
		{ID: "hot", Label: "Popular"},
		{ID: "fav:100", Label: "默认收藏夹"},
	}
	a.tabID = "hot"
	a.items = []video.Item{{ID: "BV1", Title: "First"}}
	a.page = pageDetail
	a.detailOf = "BV1"
	a.detail = &video.Video{Item: video.Item{ID: "BV1", Title: "First"}}

	tester := ui.NewTester(func(c *ui.Context) {
		a.navTabs(c)
	}, 260, 400)
	// Re-clicking the already active tab while in detail page must return to pageList.
	if err := tester.Click("Popular"); err != nil {
		t.Fatalf("Click(Popular): %v", err)
	}
	if a.page != pageList {
		t.Fatalf("page = %v, want pageList after clicking current tab from detail", a.page)
	}
	if len(a.fwd) != 1 || a.fwd[0].detailOf != "BV1" {
		t.Fatalf("forward stack = %+v, want detail BV1 saved", a.fwd)
	}

	// Re-enter detail page, then click a favorites playlist tab in the sidebar.
	a.page = pageDetail
	a.detailOf = "BV1"
	a.detail = &video.Video{Item: video.Item{ID: "BV1", Title: "First"}}
	if err := tester.Click("默认收藏夹"); err != nil {
		t.Fatalf("Click(默认收藏夹): %v", err)
	}
	if a.page != pageList || a.tabID != "fav:100" {
		t.Fatalf("page=%v tabID=%q, want pageList and fav:100", a.page, a.tabID)
	}
}

func TestPlaylistSearchAndQueuePlayback(t *testing.T) {
	a := toolApp(t)
	a.tabs = []video.Tab{
		{ID: "hot", Label: "Popular"},
		{ID: "fav:100", Label: "默认收藏夹"},
		{ID: "season:200", Label: "编程合集"},
	}
	a.tabID = "fav:100"
	a.items = []video.Item{
		{ID: "BV1", Title: "Go 语言实战", Subtitle: "UP主A"},
		{ID: "BV2", Title: "Rust 入门", Subtitle: "UP主B"},
		{ID: "BV3", Title: "Go 并发编程", Subtitle: "UP主A"},
	}

	// 1. Search within playlist filters items by title/subtitle.
	a.playlistQuery = "rust"
	got := a.playlistItems()
	if len(got) != 1 || got[0].ID != "BV2" {
		t.Fatalf("playlistItems(rust) = %+v, want [BV2]", got)
	}
	a.playlistQuery = ""

	// 2. Global playlist search matches playlist tabs by name.
	pls := a.matchingPlaylists("默认")
	if len(pls) != 1 || pls[0].ID != "fav:100" {
		t.Fatalf("matchingPlaylists(默认) = %+v, want [fav:100]", pls)
	}

	// 3. Playing a playlist queues its items and steps across videos when an episode ends.
	a.playPlaylist(a.items, 0, false)
	if a.page != pagePlay || a.playing.item.ID != "BV1" || len(a.queue) != 3 || a.queueAt != 0 {
		t.Fatalf("after playPlaylist: page=%v playing=%s queue=%d queueAt=%d", a.page, a.playing.item.ID, len(a.queue), a.queueAt)
	}
	// Single-episode video in playlist still has next episode via the playlist queue.
	a.detail = &video.Video{
		Item:     a.items[0],
		Episodes: []video.Episode{{ID: "BV1", Title: "Go 语言实战"}},
	}
	if !a.hasEpisode(1) {
		t.Fatal("hasEpisode(1) = false on first item of 3-video playlist, want true")
	}
	if a.hasEpisode(-1) {
		t.Fatal("hasEpisode(-1) = true on first item of playlist, want false")
	}
	a.nextEpisode(false)
	if a.playing.item.ID != "BV2" || a.queueAt != 1 {
		t.Fatalf("after nextEpisode(false): playing=%s queueAt=%d, want BV2 at 1", a.playing.item.ID, a.queueAt)
	}
	if !a.hasEpisode(-1) {
		t.Fatal("hasEpisode(-1) = false on second item of playlist, want true")
	}
	a.nextEpisode(true)
	if a.playing.item.ID != "BV1" || a.queueAt != 0 {
		t.Fatalf("after nextEpisode(true): playing=%s queueAt=%d, want BV1 at 0", a.playing.item.ID, a.queueAt)
	}
}

func TestDetailInlineSlotSelectAndDirectPlayOnCodie(t *testing.T) {
	a := toolApp(t)
	a.codie = codieout.New(&codieout.Creds{Addr: "127.0.0.1:1"})
	a.page = pageDetail
	a.detail = &video.Video{
		Item:     video.Item{ID: "BV1", Title: "Demo"},
		Episodes: []video.Episode{{ID: "BV1", Title: "P1"}},
	}
	tester := ui.NewTester(func(c *ui.Context) {
		a.detailActions(c, a.detail)
	}, 640, 140)
	// Choose Slot 5 inline on the detail page.
	if err := tester.Click("Slot 5"); err != nil {
		t.Fatalf("Click(Slot 5): %v", err)
	}
	if a.slotChoice != 5 {
		t.Fatalf("slotChoice = %d, want 5", a.slotChoice)
	}
	// Clicking "Play on codie" starts playback on codie immediately without any modal confirmation.
	if err := tester.Click("Play on codie"); err != nil {
		t.Fatalf("Click(Play on codie): %v", err)
	}
	if !a.deviceOutput || a.page != pagePlay || a.playing.item.ID != "BV1" {
		t.Fatalf("after Play on codie: deviceOutput=%v page=%v playing=%q", a.deviceOutput, a.page, a.playing.item.ID)
	}
}

func TestCodieMultiSegmentAndPlaylistQueueCompatibility(t *testing.T) {
	a := toolApp(t)
	a.codie = codieout.New(&codieout.Creds{Addr: "127.0.0.1:1"})
	a.deviceOutput = true
	t.Cleanup(a.cancelNext)

	// Set up a 2-video playlist queue where the first video (BV1) is split into
	// 2 segments of 10 minutes (600,000 ms) each.
	items := []video.Item{
		{ID: "BV1", Title: "长视频1"},
		{ID: "BV2", Title: "长视频2"},
	}
	a.setQueue(items, 0, "默认收藏夹")
	a.playing = playing{item: items[0], ep: video.Episode{ID: "BV1", Title: "长视频1"}, at: 0}
	a.detail = &video.Video{Item: items[0], Episodes: []video.Episode{{ID: "BV1", Title: "长视频1"}}}
	a.codieSegs = []codieSegment{
		{path: "seg0.mp4", startMS: 0, durMS: 600_000},
		{path: "seg1.mp4", startMS: 600_000, durMS: 600_000},
	}
	a.codieSegAt = 0
	a.codieTotalMS = 1_200_000
	a.codieSession = "sess-seg0"

	opened := make(chan string, 4)
	oldOpen := codieOpenSessionFn
	codieOpenSessionFn = func(c *codieout.Client, path string, req codieout.MediaSessionRequest) (string, error) {
		start := int64(0)
		if req.Playback != nil {
			start = req.Playback.StartPositionMS
		}
		tag := fmt.Sprintf("%s@%d", path, start)
		opened <- tag
		return "sess-" + path, nil
	}
	t.Cleanup(func() { codieOpenSessionFn = oldOpen })

	// 1. Segment 0 ends: codieEnded must NOT start the 8-second next-episode
	// countdown; it must seamlessly switch to segment 1.
	a.codieState = &codieout.MediaSessionState{IsPlaying: false, PositionMS: 599_500, DurationMS: 600_000}
	a.codieEnded()
	if a.nextLatch || a.nextLeft != 0 {
		t.Fatalf("segment 0 ended: nextLatch=%v nextLeft=%d, want no next-episode countdown yet", a.nextLatch, a.nextLeft)
	}
	select {
	case got := <-opened:
		if got != "seg1.mp4@0" {
			t.Fatalf("opened segment = %q, want seg1.mp4@0", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for segment 1 to open")
	}
	// Wait briefly for apply() to finish on the goroutine.
	for i := 0; i < 50 && a.codieSegAt != 1; i++ {
		time.Sleep(10 * time.Millisecond)
	}
	if a.codieSegAt != 1 || a.codieSession != "sess-seg1.mp4" {
		t.Fatalf("after segment switch: segAt=%d session=%q", a.codieSegAt, a.codieSession)
	}

	// 2. Unified timeline on segment 1: 150s into segment 1 is 750s / 1200s total.
	a.codieState = &codieout.MediaSessionState{IsPlaying: true, PositionMS: 150_000, DurationMS: 600_000}
	pos, dur := a.codieTimes()
	if pos != 750*time.Second || dur != 1200*time.Second {
		t.Fatalf("codieTimes on seg 1 = (%v, %v), want (750s, 1200s)", pos, dur)
	}

	// 3. Cross-segment seek back to 100s (in segment 0) switches back to seg0.mp4@100000.
	a.codieSeek(100_000)
	select {
	case got := <-opened:
		if got != "seg0.mp4@100000" {
			t.Fatalf("cross-segment seek opened = %q, want seg0.mp4@100000", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for cross-segment seek")
	}
	for i := 0; i < 50 && a.codieSegAt != 0; i++ {
		time.Sleep(10 * time.Millisecond)
	}

	// 4. Advance to last segment (seg 1) and finish it: now the whole episode is
	// done, so codieEnded must trigger the countdown to the next playlist video (BV2).
	a.codieSegAt = 1
	a.codieState = &codieout.MediaSessionState{IsPlaying: false, PositionMS: 599_500, DurationMS: 600_000}
	a.codieEnded()
	if !a.nextLatch || a.nextLeft != 8 {
		t.Fatalf("last segment ended: nextLatch=%v nextLeft=%d, want countdown to next playlist item", a.nextLatch, a.nextLeft)
	}
	a.nextEpisode(false)
	if a.playing.item.ID != "BV2" || a.queueAt != 1 {
		t.Fatalf("after nextEpisode from split video: playing=%q queueAt=%d, want BV2 at 1", a.playing.item.ID, a.queueAt)
	}
}





func TestTakeCodiePrepared(t *testing.T) {
	// The file readied for the playlist's next video plays for that very
	// episode at the speed it was made at, and only once: another video, or
	// another speed, would show the wrong thing at the wrong pace.
	a := &app{rate: 1, prepared: &codiePrepared{source: "youtube", item: "a", ep: "1", rate: 1, path: "next.mp4"}}
	if _, _, ok := a.takeCodiePrepared(video.Item{ID: "b", Source: "youtube"}, video.Episode{ID: "1"}); ok {
		t.Error("a file made for another video was played")
	}
	a.rate = 1.5
	if _, _, ok := a.takeCodiePrepared(video.Item{ID: "a", Source: "youtube"}, video.Episode{ID: "1"}); ok {
		t.Error("a file made at 1× was played at 1.5×")
	}
	a.rate = 1
	if p, rate, ok := a.takeCodiePrepared(video.Item{ID: "a", Source: "youtube"}, video.Episode{ID: "1"}); !ok || p != "next.mp4" || rate != 1 {
		t.Fatalf("the readied file wasn't taken: %q %v %v", p, rate, ok)
	}
	if _, _, ok := a.takeCodiePrepared(video.Item{ID: "a", Source: "youtube"}, video.Episode{ID: "1"}); ok {
		t.Error("the same file was handed out twice")
	}
}

func TestCodieStreamStopsWithThePlayback(t *testing.T) {
	// A stopped playback stops making segments: a user who leaves an episode
	// mid-way must not leave ffmpeg working on the rest of it.
	dir := t.TempDir()
	fake := filepath.Join(dir, "ffmpeg")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nexec sleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	old := findFFmpegFn
	findFFmpegFn = func() string { return fake }
	defer func() { findFFmpegFn = old }()

	a := &app{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		a.codieStream(ctx, &video.Stream{URL: "http://127.0.0.1:1/x"}, 1, 0, 600, 1, nil)
		close(done)
	}()
	time.Sleep(150 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("the segment maker outlived the playback it was for")
	}
}

// seamApp is an app on the device with a two-segment episode: segment 0
// playing, segment 1 made and waiting, and a stub where the session opens.
func seamApp(t *testing.T) (*app, chan string) {
	t.Helper()
	a := toolApp(t)
	a.codie = codieout.New(&codieout.Creds{Addr: "127.0.0.1:1"})
	a.deviceOutput = true
	t.Cleanup(a.cancelNext)
	a.codieSegs = []codieSegment{
		{path: "seg0.mp4", startMS: 0, durMS: 600_000},
		{path: "seg1.mp4", startMS: 600_000, durMS: 600_000},
	}
	a.codieSegAt = 0
	a.codieSession = "sess-seg0"
	opened := make(chan string, 4)
	oldOpen := codieOpenSessionFn
	codieOpenSessionFn = func(c *codieout.Client, path string, req codieout.MediaSessionRequest) (string, error) {
		opened <- path
		return "sess-" + path, nil
	}
	t.Cleanup(func() { codieOpenSessionFn = oldOpen })
	return a, opened
}

func TestCodieSeamSwitchesWhilePlaying(t *testing.T) {
	// The seam: with the next segment made and this one still playing inside
	// the switch window, the next upload starts before this one ends — no
	// black beat, and no next-episode countdown mid-episode either.
	a, opened := seamApp(t)
	a.codieState = &codieout.MediaSessionState{IsPlaying: true, PositionMS: 597_000, DurationMS: 600_000}
	a.codieSeamCheck(a.codieState)
	select {
	case got := <-opened:
		if got != "seg1.mp4" {
			t.Fatalf("opened %q, want seg1.mp4", got)
		}
	default:
		t.Fatal("the next segment's session never opened")
	}
	if a.codieSegAt != 1 {
		t.Fatalf("segAt = %d, want 1 after the seam switch", a.codieSegAt)
	}
	if a.codieSeam {
		t.Error("the seam was still lit after the next segment landed")
	}
	if a.nextLatch {
		t.Error("mid-episode, but the next-episode countdown came")
	}
}

func TestCodieSeamWaitsFarFromTheEnd(t *testing.T) {
	// Far from the end nothing opens: the window is the last few seconds only.
	a, opened := seamApp(t)
	a.codieState = &codieout.MediaSessionState{IsPlaying: true, PositionMS: 60_000, DurationMS: 600_000}
	a.codieSeamCheck(a.codieState)
	select {
	case got := <-opened:
		t.Fatalf("opened %q with 9 minutes left", got)
	default:
	}
	if a.codieSeam || a.codieSwitchingSeg {
		t.Error("the seam lit with 9 minutes left")
	}
}

func TestCodieSeamLightsWhileNextIsMade(t *testing.T) {
	// Inside the window but the next file isn't made yet: nothing opens, but
	// the stage says the next part is coming instead of going silent.
	a, opened := seamApp(t)
	a.codieSegs = a.codieSegs[:1] // 第二段还没生成
	a.codieState = &codieout.MediaSessionState{IsPlaying: true, PositionMS: 597_000, DurationMS: 600_000}
	a.codieSeamCheck(a.codieState)
	select {
	case got := <-opened:
		t.Fatalf("opened %q with no second segment made", got)
	default:
	}
	if !a.codieSeam {
		t.Error("no loading line while the next part is being made")
	}
}

func TestCodieSeamHoldsAPause(t *testing.T) {
	// A pause pressed while the next segment uploads follows it over: the new
	// session opens paused, not playing over the user's pause.
	a, opened := seamApp(t)
	var actions []string
	oldAction := codieMediaAction
	codieMediaAction = func(c *codieout.Client, session, action string, args map[string]any) {
		actions = append(actions, action)
	}
	t.Cleanup(func() { codieMediaAction = oldAction })

	// 换段还在路上时按了暂停:旧会话停在半路。
	a.codieState = &codieout.MediaSessionState{IsPlaying: false, PositionMS: 300_000, DurationMS: 600_000}
	a.codieSwitchSegment(a.codieGen, 1, 0)
	if got := <-opened; got != "seg1.mp4" {
		t.Fatalf("opened %q, want seg1.mp4", got)
	}
	if a.codieSegAt != 1 {
		t.Fatalf("segAt = %d, want 1", a.codieSegAt)
	}
	if len(actions) != 1 || actions[0] != "pause" {
		t.Fatalf("actions on the new session = %v, want one pause", actions)
	}
	if a.codieState == nil || a.codieState.IsPlaying {
		t.Error("the new session was left playing over a pause")
	}
	// 播完触发的换段不算暂停:新会话照常放。
	a.codieState = &codieout.MediaSessionState{IsPlaying: false, PositionMS: 599_500, DurationMS: 600_000}
	a.codieSwitchSegment(a.codieGen, 0, 0)
	if got := <-opened; got != "seg0.mp4" {
		t.Fatalf("opened %q, want seg0.mp4", got)
	}
	if len(actions) != 1 {
		t.Fatalf("actions after an end-of-segment switch = %v, want none new", actions)
	}
	if a.codieState == nil || !a.codieState.IsPlaying {
		t.Error("an end-of-segment switch left the new session paused")
	}
}

func TestCodieSwitchWindow(t *testing.T) {
	// The window grows with what uploads have been taking, and never shrinks
	// under two seconds: a too-early start shaves the tail, a too-late one
	// shows black.
	a := &app{}
	if got := a.codieSwitchWindow(); got != codieUploadSeed+codieSeamMargin {
		t.Errorf("unmeasured window = %v, want seed+margin %v", got, codieUploadSeed+codieSeamMargin)
	}
	a.codieUploadEMA = 100
	if got := a.codieSwitchWindow(); got != 2*time.Second {
		t.Errorf("window under a fast upload = %v, want the 2s floor", got)
	}
	a.codieUploadEMA = 10_000
	if got := a.codieSwitchWindow(); got != 10_800*time.Millisecond {
		t.Errorf("window over a 10s upload = %v, want 10.8s", got)
	}
	a.codieUploadTook(20 * time.Second)
	if got := a.codieUploadEMA; got != 15_000 {
		t.Errorf("EMA after a 20s upload = %v, want 15s (half of 10s and 20s)", got)
	}
}

func TestCodieTimesShowsWholeEpisode(t *testing.T) {
	// The bar and the clock speak of the whole video from the first second:
	// the probe knew the episode's length before any but the first segment
	// was made, and position carries the finished segments' share with it.
	a := &app{}
	a.codieState = &codieout.MediaSessionState{IsPlaying: true, PositionMS: 10_000, DurationMS: 30_000}
	a.codieSegs = []codieSegment{{path: "s0.mp4", startMS: 0, durMS: 30_000}}
	a.codieEpisodeMS = 1_200_000
	pos, dur := a.codieTimes()
	if pos != 10*time.Second || dur != 1200*time.Second {
		t.Fatalf("one made segment: (%v, %v), want (10s, 1200s)", pos, dur)
	}
	a.codieSegs = append(a.codieSegs, codieSegment{path: "s1.mp4", startMS: 30_000, durMS: 30_000})
	a.codieSegAt = 1
	a.codieState = &codieout.MediaSessionState{IsPlaying: true, PositionMS: 5_000, DurationMS: 30_000}
	pos, dur = a.codieTimes()
	if pos != 35*time.Second || dur != 1200*time.Second {
		t.Fatalf("into segment 1: (%v, %v), want (35s, 1200s)", pos, dur)
	}
}

func TestCodieSeekClampsToMadeSegments(t *testing.T) {
	// Dragging into a part that isn't made yet stops at the made segment's
	// tail instead of seeking past its end into nothing.
	a := toolApp(t)
	a.codie = codieout.New(&codieout.Creds{Addr: "127.0.0.1:1"})
	a.codieSegs = []codieSegment{{path: "seg0.mp4", startMS: 0, durMS: 600_000}}
	a.codieSegAt = 0
	a.codieSession = "sess-seg0"
	seeked := make(chan int64, 4)
	oldAction := codieMediaAction
	codieMediaAction = func(c *codieout.Client, session, action string, args map[string]any) {
		if action == "seek" {
			seeked <- args["position_ms"].(int64)
		}
	}
	t.Cleanup(func() { codieMediaAction = oldAction })

	// 这一集全长 20 分钟,但只生成了前 10 分钟:拖到 19 分钟处,落在已生成段末尾前。
	a.codieEpisodeMS = 1_200_000
	a.codieSeek(1_150_000)
	select {
	case got := <-seeked:
		if got != 599_500 {
			t.Fatalf("seek into the unmade tail landed at %d, want clamped to 599500", got)
		}
	default:
		t.Fatal("no seek went out")
	}
	if a.codieSegAt != 0 {
		t.Fatalf("segAt = %d, a clamped seek shouldn't have switched segments", a.codieSegAt)
	}
}

func TestCodieTargetSize(t *testing.T) {
	tests := []struct {
		maxHeight int
		w, h      int
	}{
		{0, 1920, 1080},   // auto: the device's own cap
		{1080, 1920, 1080},
		{720, 1280, 720},
		{480, 852, 480},
		{2160, 1920, 1080}, // never past what the device takes
		{-5, 1920, 1080},
	}
	for _, tt := range tests {
		w, h := codieTargetSize(tt.maxHeight)
		if w != tt.w || h != tt.h {
			t.Errorf("codieTargetSize(%d) = %dx%d, want %dx%d", tt.maxHeight, w, h, tt.w, tt.h)
		}
	}
}

// The resolution choice shapes the picture: 480 makes a 854x480 episode, not
// the usual 1080p one.
func TestCodieArgsFollowResolution(t *testing.T) {
	s := &video.Stream{URL: "http://x/v.mp4", Type: "mp4"}
	for _, tt := range []struct {
		maxHeight int
		scale     string
	}{
		{720, "scale=1280:720:force_original_aspect_ratio=decrease,pad=ceil(iw/2)*2:ceil(ih/2)*2"},
		{480, "scale=852:480:force_original_aspect_ratio=decrease,pad=ceil(iw/2)*2:ceil(ih/2)*2"},
		{0, "scale=1920:1080:force_original_aspect_ratio=decrease,pad=ceil(iw/2)*2:ceil(ih/2)*2"},
	} {
		got := codieFFmpegArgs(s, 1, false, "out.mp4", tt.maxHeight)
		if !hasPair(got, "-vf", tt.scale) {
			t.Errorf("maxHeight %d = %v, want -vf %s", tt.maxHeight, got, tt.scale)
		}
	}
}

// The device's session is always the whole screen with the picture shown
// whole: the codie screen is its own screen, not a window to arrange.
func TestCodieLayoutIsFullscreen(t *testing.T) {
	a := &app{}
	l := a.codieLayout()
	if l.Fit != "contain" || l.WidthPercent != 100 || l.HeightPercent != 100 {
		t.Errorf("layout = %+v, want a full-screen contain", l)
	}
}

// The device turning the first segment's session away must not leave the
// stage saying "Transcoding…" forever: the busy line goes, and the device's
// own reason takes its place.
func TestCodieStreamSurfacesSessionRefusal(t *testing.T) {
	dir := t.TempDir()
	fake := filepath.Join(dir, "ffmpeg")
	script := "#!/bin/sh\n# a tiny valid mp4-shaped answer: the maker only stats the file\nhead -c 65536 /dev/zero > \"$0.out\"\nexit 0\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	// codieMakeSegment stats dst (its own temp file) and probes its duration;
	// a file of zeros has no moov, but the probe failing only logs. The
	// session open below fails regardless.
	oldFF := findFFmpegFn
	findFFmpegFn = func() string { return fake }
	defer func() { findFFmpegFn = oldFF }()

	a := &app{}
	a.settings = &settings{}
	a.codieBusy = "Transcoding for the device…"
	gen := a.codieGen

	saved := codieOpenSessionFn
	codieOpenSessionFn = func(*codieout.Client, string, codieout.MediaSessionRequest) (string, error) {
		return "", errors.New("codie: MEDIA_SESSION_BLOCKED: The required output is unavailable. (HTTP 409)——把设备屏幕点亮、解锁再试")
	}
	defer func() { codieOpenSessionFn = saved }()

	done := make(chan struct{})
	go func() {
		a.codieStream(context.Background(), &video.Stream{URL: "/tmp/x.mp4"}, 1, 0, 60, gen, nil)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("codieStream never came back after the refusal")
	}
	if a.codieBusy != "" {
		t.Errorf("busy = %q, want it cleared", a.codieBusy)
	}
	if a.playerErr == "" || !strings.Contains(a.playerErr, "MEDIA_SESSION_BLOCKED") {
		t.Errorf("playerErr = %q, want the device's own reason", a.playerErr)
	}
}
