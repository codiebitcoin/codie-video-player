package app

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	mrand "math/rand/v2"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo"

	clt01 "workshop_inner_sdk"

	"codie-video-player/internal/codieout"
	"codie-video-player/internal/library"
	"codie-video-player/internal/video"
)

// 对话控制:给配对的 codie 设备注册一组工具目录,再守住设备的执行通道。
// 设备那边的对话模型看得见这些工具,观众对它说的"下一个""暂停""播放某某剧"
// 就落成这里的调用,由 app 执行,结果回给模型继续对话。
//
// 通道与目录的协议都在 third_party/workshop_inner_sdk(firmware 1.6.0,
// codie-life-connect 同款实测);这里的活是目录长什么样、每件活怎么干。

// readOnMain runs fn on the main thread and waits, a var so tests don't
// need a loop running to read the state.
var readOnMain = func(fn func()) {
	mygo.RunOnMain(fn)
}

// agentTools is one running conversation-tool service for the paired
// device; zero value while the viewer keeps it off.
type agentTools struct {
	cancel context.CancelFunc
	srv    *clt01.ToolServer

	mu     sync.Mutex
	calls  int // invocations served, for the settings row
	lastIn time.Time
}

// agentIntegrationID is the tool catalog's identity on the device: stable
// across runs, so a re-register updates the catalog instead of doubling it.
var agentIntegrationID = clt01.StableIntegrationID("codie-video-player")

// toolSearchWait is how long search_play waits for a source to answer
// before it owns up to not finding anything.
var toolSearchWait = 6 * time.Second

// toolsRefresh brings the conversation tools up or down to match what the
// viewer asked for and whether a device is paired. Runs on the main thread.
func (a *app) toolsRefresh() {
	want := a.settings.ConversationControl && a.codie != nil
	switch {
	case want && a.tools == nil:
		a.tools = a.startAgentTools()
	case !want && a.tools != nil:
		a.tools.stop()
		a.tools = nil
	}
	if a.win != nil {
		a.win.Invalidate()
	}
}

// stop tears the service down: the device marks the catalog offline, and
// its conversation stops offering the tools.
func (t *agentTools) stop() {
	if t.cancel != nil {
		t.cancel()
	}
}

// toolsCalls is how many tool calls the device has asked of the player, for
// the settings row that says the channel is alive.
func (a *app) toolsCalls() int {
	if a.tools == nil {
		return 0
	}
	a.tools.mu.Lock()
	defer a.tools.mu.Unlock()
	return a.tools.calls
}

// startAgentTools builds the client from the pairing's credentials,
// registers the catalog, and holds the executor open. It never blocks the
// caller for the network: registration retries in the background, and the
// SDK reconnects the channel itself.
func (a *app) startAgentTools() *agentTools {
	creds := a.codie.Creds
	c, err := clt01.New(codieout.NormalizeAddr(creds.Addr))
	if err != nil {
		log.Printf("agent tools: client: %v", err)
		return nil
	}
	c.Secret = creds.Secret
	c.SessionID = creds.SessionID
	c.UserID = creds.UserID
	ctx, cancel := context.WithCancel(context.Background())
	t := &agentTools{cancel: cancel}
	opt := clt01.ToolServerOptions{
		IntegrationID: agentIntegrationID,
		Name:          "Codie Video Player",
		Enabled:       true,
		Tools:         a.agentToolCatalog(),
		OnResult: func(inv clt01.Invocation, status string, err error) {
			t.mu.Lock()
			t.calls++
			t.lastIn = time.Now()
			t.mu.Unlock()
			log.Printf("agent tools: %s -> %s (err=%v)", inv.Name, status, err)
		},
	}
	go func() {
		// Registration is plain signed HTTP; a sleeping device misses the
		// first asks, so it tries for a bit before giving up.
		var srv *clt01.ToolServer
		var err error
		for i := 0; i < 4; i++ {
			if ctx.Err() != nil {
				return
			}
			srv, err = c.ServeTools(ctx, opt)
			if err == nil {
				break
			}
			log.Printf("agent tools: register: %v (try %d)", err, i+1)
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
			}
		}
		if err != nil {
			log.Printf("agent tools: giving up registering: %v", err)
			return
		}
		log.Printf("agent tools: catalog registered, holding executor open")
		srv.Start(ctx)
	}()
	return t
}

// agentToolCatalog is what the device's model may ask of the player. The
// descriptions read in Chinese, for that is what the talk around them reads
// in; every handler returns fast and does its slow work off the tool's
// deadline, since the device fails a call that runs past it.
func (a *app) agentToolCatalog() []clt01.Tool {
	okSchema := objSchema(map[string]any{"ok": map[string]any{"type": "boolean"}}, "ok")
	noArgs := objSchema(map[string]any{})
	speeds := []any{0.5, 0.75, 1, 1.25, 1.5, 2}
	return []clt01.Tool{
		{
			Name:        "whats_playing",
			Version:     "v1",
			Description: "查询当前在播什么:剧名、第几集、播到哪、在本机还是设备上。不需要参数。",
			Parameters:  noArgs,
			ResultSchema: objSchema(map[string]any{
				"title":     sSchema,
				"episode":   sSchema,
				"ep_number": map[string]any{"type": "integer"},
				"ep_total":  map[string]any{"type": "integer"},
				"position_s": map[string]any{"type": "integer",
					"description": "本集已播秒数"},
				"on_device": map[string]any{"type": "boolean",
					"description": "true 在 codie 设备屏幕上,false 在电脑窗口里"},
				"playing": map[string]any{"type": "boolean"},
				"rate":    map[string]any{"type": "number"},
			}),
			Risk: "read", Idempotent: true, TimeoutMS: 3000,
			Handle: func(ctx context.Context, args json.RawMessage) (any, error) {
				var st playState
				readOnMain(func() { st = a.playState() })
				if st.Title == "" {
					return nil, &clt01.ToolError{Code: "NOTHING_PLAYING", Message: "现在没有在播放"}
				}
				return st, nil
			},
		},
		{
			Name:        "pause",
			Version:     "v1",
			Description: "暂停当前播放的视频。",
			Parameters:  noArgs,
			ResultSchema: objSchema(map[string]any{
				"ok": map[string]any{"type": "boolean"},
			}, "ok"),
			Risk: "reversible", Idempotent: true, TimeoutMS: 3000,
			Handle: func(ctx context.Context, args json.RawMessage) (any, error) {
				return a.toolsPlayStateOnMain(func() bool { return a.pausePlaying() })
			},
		},
		{
			Name:         "resume",
			Version:      "v1",
			Description:  "继续播放已暂停的视频。",
			Parameters:   noArgs,
			ResultSchema: okSchema,
			Risk:         "reversible", Idempotent: true, TimeoutMS: 3000,
			Handle: func(ctx context.Context, args json.RawMessage) (any, error) {
				return a.toolsPlayStateOnMain(func() bool { return a.resumePlaying() })
			},
		},
		{
			Name:        "next_episode",
			Version:     "v1",
			Description: "播下一集。没有下一集时会说明。",
			Parameters:  noArgs,
			ResultSchema: objSchema(map[string]any{
				"ok":      map[string]any{"type": "boolean"},
				"episode": sSchema,
			}),
			Risk: "reversible", Idempotent: false, TimeoutMS: 5000,
			Handle: func(ctx context.Context, args json.RawMessage) (any, error) {
				return a.toolsStepEpisode(false)
			},
		},
		{
			Name:        "previous_episode",
			Version:     "v1",
			Description: "播上一集。没有上一集时会说明。",
			Parameters:  noArgs,
			ResultSchema: objSchema(map[string]any{
				"ok":      map[string]any{"type": "boolean"},
				"episode": sSchema,
			}),
			Risk: "reversible", Idempotent: false, TimeoutMS: 5000,
			Handle: func(ctx context.Context, args json.RawMessage) (any, error) {
				return a.toolsStepEpisode(true)
			},
		},
		{
			Name:        "search_play",
			Version:     "v1",
			Description: "按名字搜索一部剧并立刻开始播放它的第一集。query 是剧名或关键词。工具返回时播放已经开始排队,画面出现还要再等一会(设备播放需要先转码)。",
			Parameters: objSchema(map[string]any{
				"query": map[string]any{"type": "string", "description": "剧名或搜索词"},
			}, "query"),
			ResultSchema: objSchema(map[string]any{
				"found":   map[string]any{"type": "boolean"},
				"title":   sSchema,
				"episode": sSchema,
				"source":  sSchema,
			}, "found"),
			Risk: "reversible", Idempotent: false, TimeoutMS: 10000,
			Handle: func(ctx context.Context, args json.RawMessage) (any, error) {
				var q struct {
					Query string `json:"query"`
				}
				if err := json.Unmarshal(args, &q); err != nil || strings.TrimSpace(q.Query) == "" {
					return nil, clt01.Clarify("没听清剧名,请再说一次要播什么")
				}
				return a.toolsSearchPlay(ctx, strings.TrimSpace(q.Query))
			},
		},
		{
			Name:        "recommend",
			Version:     "v1",
			Description: "推荐一部剧并立刻开始播放:先看追剧里没看完的,再从当前分类的热门里挑没看过的。不需要参数。",
			Parameters:  noArgs,
			ResultSchema: objSchema(map[string]any{
				"found":   map[string]any{"type": "boolean"},
				"title":   sSchema,
				"episode": sSchema,
				"source":  sSchema,
				"reason": map[string]any{"type": "string",
					"description": "为什么推荐它:追剧 或 热门"},
			}, "found"),
			Risk: "reversible", Idempotent: false, TimeoutMS: 10000,
			Handle: func(ctx context.Context, args json.RawMessage) (any, error) {
				return a.toolsRecommend(ctx)
			},
		},
		{
			Name:        "random_play",
			Version:     "v1",
			Description: "随机挑一部视频立刻播放:从当前分类里随便取一个没看过的。观众说「随机播放一个视频」「随便放一个」「放点东西看看」「random video」「something random」「ランダムに再生」时用它。不需要参数。",
			Parameters:  noArgs,
			ResultSchema: objSchema(map[string]any{
				"found":   map[string]any{"type": "boolean"},
				"title":   sSchema,
				"episode": sSchema,
				"source":  sSchema,
			}, "found"),
			Risk: "reversible", Idempotent: false, TimeoutMS: 20000,
			Handle: func(ctx context.Context, args json.RawMessage) (any, error) {
				return a.toolsPickPlay(ctx, false)
			},
		},
		{
			Name:        "hot_play",
			Version:     "v1",
			Description: "播放当前源最热门的视频。观众说「播放最热视频」「放个最火的」「看看热门」「play the hottest」「show me what's popular」「一番人気を再生」时用它。不需要参数。",
			Parameters:  noArgs,
			ResultSchema: objSchema(map[string]any{
				"found":   map[string]any{"type": "boolean"},
				"title":   sSchema,
				"episode": sSchema,
				"source":  sSchema,
			}, "found"),
			Risk: "reversible", Idempotent: false, TimeoutMS: 20000,
			Handle: func(ctx context.Context, args json.RawMessage) (any, error) {
				return a.toolsPickPlay(ctx, true)
			},
		},
		{
			Name:        "set_speed",
			Version:     "v1",
			Description: "调整播放速度(倍速)。在电脑上立刻生效;在 codie 设备上需要重新转码,画面稍后才按新速度播。",
			Parameters: objSchema(map[string]any{
				"rate": map[string]any{"type": "number", "enum": speeds,
					"description": "倍速,1 是原速"},
			}, "rate"),
			ResultSchema: objSchema(map[string]any{
				"ok":   map[string]any{"type": "boolean"},
				"rate": map[string]any{"type": "number"},
			}, "ok"),
			Risk: "reversible", Idempotent: true, TimeoutMS: 5000,
			Handle: func(ctx context.Context, args json.RawMessage) (any, error) {
				var q struct {
					Rate float64 `json:"rate"`
				}
				if err := json.Unmarshal(args, &q); err != nil {
					return nil, &clt01.ToolError{Code: "BAD_ARGS", Message: "速度参数没读懂"}
				}
				if !validRate(q.Rate) {
					return nil, &clt01.ToolError{Code: "BAD_RATE", Message: "速度只支持 0.5 到 2 之间菜单里的档位"}
				}
				readOnMain(func() { a.setRate(q.Rate) })
				return map[string]any{"ok": true, "rate": q.Rate}, nil
			},
		},
	}
}

// sSchema is an optional string field of a result.
var sSchema = map[string]any{"type": "string"}

// objSchema makes one of the device's schema objects: what its fields are,
// which must be there, and nothing allowed beyond them.
func objSchema(props map[string]any, required ...string) []byte {
	m := map[string]any{"type": "object", "properties": props, "additionalProperties": false}
	if len(required) > 0 {
		m["required"] = required
	}
	b, _ := json.Marshal(m)
	return b
}

// validRate says whether r is a speed the rate menu offers.
func validRate(r float64) bool {
	for _, v := range []float64{0.5, 0.75, 1, 1.25, 1.5, 2} {
		if r == v {
			return true
		}
	}
	return false
}

// playState is what's on now, as the conversation asks of it. It answers in
// story seconds: the device's file clock runs at the rate, the viewer's
// sense of "播到哪" doesn't.
type playState struct {
	Title     string  `json:"title,omitempty"`
	Episode   string  `json:"episode,omitempty"`
	EpNumber  int     `json:"ep_number,omitempty"`
	EpTotal   int     `json:"ep_total,omitempty"`
	PositionS int     `json:"position_s,omitempty"`
	OnDevice  bool    `json:"on_device,omitempty"`
	Playing   bool    `json:"playing,omitempty"`
	Rate      float64 `json:"rate,omitempty"`
}

// playState reads what's on, on the main thread.
func (a *app) playState() playState {
	st := playState{Rate: a.rate, OnDevice: a.deviceOutput}
	p := a.playing
	if p.item.ID == "" {
		return st
	}
	st.Title = p.item.Title
	st.Episode = p.ep.Title
	st.EpNumber = p.at + 1
	if d := a.detail; d != nil {
		st.EpTotal = len(d.Episodes)
	}
	if a.deviceOutput {
		pos, _ := a.codieTimes()
		st.PositionS = int(pos.Seconds() * a.rate)
		st.Playing = a.codieState != nil && a.codieState.IsPlaying
	} else if a.player != nil {
		st.PositionS = int(a.player.Position().Seconds())
		st.Playing = a.player.Playing()
	}
	return st
}

// toolsPlayStateOnMain runs fn on the main thread and answers the state
// after it; the device's model gets to say what became of the ask.
func (a *app) toolsPlayStateOnMain(fn func() bool) (any, error) {
	var st playState
	var ok bool
	readOnMain(func() {
		ok = fn()
		st = a.playState()
	})
	if !ok {
		return nil, &clt01.ToolError{Code: "NOTHING_PLAYING", Message: "现在没有在播放"}
	}
	out := map[string]any{"ok": true}
	if st.Episode != "" {
		out["episode"] = st.Episode
	}
	return out, nil
}

// pausePlaying pauses what plays, here or on the device.
func (a *app) pausePlaying() bool {
	if a.deviceOutput && a.codie != nil {
		if a.codieSession == "" {
			return false
		}
		codieMediaAction(a.codie, a.codieSession, "pause", nil)
		return true
	}
	if a.player == nil {
		return false
	}
	a.player.Pause()
	return true
}

// resumePlaying resumes what plays, here or on the device.
func (a *app) resumePlaying() bool {
	if a.deviceOutput && a.codie != nil {
		if a.codieSession == "" {
			return false
		}
		codieMediaAction(a.codie, a.codieSession, "resume", nil)
		return true
	}
	if a.player == nil {
		return false
	}
	a.player.Play()
	return true
}

// toolsStepEpisode moves to the next or previous episode on the main
// thread, answering what plays now.
func (a *app) toolsStepEpisode(back bool) (any, error) {
	var st playState
	var ok bool
	readOnMain(func() {
		if a.playing.item.ID == "" {
			return
		}
		wasItem, wasAt := a.playing.item.ID, a.playing.at
		a.cancelNext()
		a.nextEpisode(back)
		ok = a.playing.item.ID != wasItem || a.playing.at != wasAt
		st = a.playState()
	})
	if !ok {
		return nil, &clt01.ToolError{Code: "NO_EPISODE", Message: "没有那一集可播"}
	}
	out := map[string]any{"ok": true, "episode": st.Episode}
	if st.Episode == "" {
		out["episode"] = fmt.Sprintf("第 %d 集", st.EpNumber)
	}
	return out, nil
}

// toolsSearchPlay asks every source what q is, takes the first drama of the
// first source that answers with one, and starts it at its first episode —
// on whatever output the app plays to now. The search runs here (the tool's
// own goroutine, from sources copied off the main thread's list); only the
// starting of it crosses to the main thread, so the answer beats the
// device's deadline even when a transcode follows.
func (a *app) toolsSearchPlay(ctx context.Context, q string) (any, error) {
	var entries []sourceEntry
	readOnMain(func() {
		entries = append([]sourceEntry(nil), a.sources...)
	})
	var searchable []sourceEntry
	for i := range entries {
		if entries[i].src.Can("videoSearch") {
			searchable = append(searchable, entries[i])
		}
	}
	if len(searchable) == 0 {
		return map[string]any{"found": false}, nil
	}
	// One ask to a source, all at once; the first drama found wins.
	type answer struct {
		src   sourceEntry
		items []video.Item
	}
	answers := make(chan answer, len(searchable))
	for _, s := range searchable {
		go func(s sourceEntry) {
			r, err := s.src.Search(q, 0)
			if err != nil {
				return
			}
			answers <- answer{s, r.Items}
		}(s)
	}
	var pick video.Item
	var pickSrc sourceEntry
	var found bool
	timer := time.NewTimer(toolSearchWait)
	defer timer.Stop()
	for !found {
		select {
		case <-ctx.Done():
			return nil, &clt01.ToolError{Code: "TIMEOUT", Message: "搜索超时了,请稍后再试"}
		case <-timer.C:
			return map[string]any{"found": false}, nil
		case an := <-answers:
			for i := range an.items {
				if an.items[i].ID == "" {
					continue
				}
				pick, pickSrc, found = an.items[i], an.src, true
				pick.Source = an.src.id
				break
			}
		}
	}
	// The drama whole, for its episodes; the ask rides the deadline's ctx.
	v, err := pickSrc.src.Video(pick.ID)
	if err != nil {
		return nil, &clt01.ToolError{Code: "LOOKUP_FAILED", Message: "剧找到了,但拿不到它的剧集"}
	}
	if v == nil || len(v.Episodes) == 0 {
		return map[string]any{"found": false}, nil
	}
	item, ep, at := pick, v.Episodes[0], 0
	var st playState
	readOnMain(func() {
		a.cancelNext()
		a.playEpisode(item, ep, at)
		st = a.playState()
	})
	out := map[string]any{"found": true, "title": st.Title, "source": pickSrc.name}
	if st.Episode != "" {
		out["episode"] = st.Episode
	}
	return out, nil
}

// toolsRecommend picks a drama to play the way a friend would: first the
// viewer's own follows that aren't finished, then the current tab's page of
// dramas, skipping what they've already watched, and starts it like
// search_play does. The slow asks ride the tool's own goroutine; only the
// starting crosses to the main thread.
func (a *app) toolsRecommend(ctx context.Context) (any, error) {
	// What the main thread holds: the library's words on what was watched,
	// and the source whose page to pick from.
	var follows, history []library.Watched
	var src *sourceEntry
	var tabID string
	readOnMain(func() {
		if a.lib != nil && a.sourceEntry != nil {
			follows, history = a.lib.Of(a.sourceEntry.id)
		}
		if a.sourceEntry != nil {
			s := *a.sourceEntry
			src = &s
		}
		tabID = a.tabID
	})

	// A follow that isn't finished is the pick: it plays on from where it
	// was, which playEpisode knows to do.
	for i := range follows {
		if !follows[i].Done && follows[i].ID != "" {
			item, ep, at := follows[i].Item, video.Episode{ID: follows[i].EpID, Title: follows[i].Ep}, follows[i].EpAt
			var st playState
			readOnMain(func() {
				a.cancelNext()
				a.playEpisode(item, ep, at)
				st = a.playState()
			})
			out := map[string]any{"found": true, "title": st.Title, "reason": "追剧", "source": item.Source}
			if st.Episode != "" {
				out["episode"] = st.Episode
			}
			return out, nil
		}
	}

	// Nothing to go on with: the current tab's page names something new.
	if src == nil || !src.src.Can("videoList") {
		return map[string]any{"found": false}, nil
	}
	seen := map[string]bool{}
	for i := range history {
		seen[history[i].ID] = true
	}
	tab := tabID
	if tab == "" {
		tabs, err := src.src.Tabs()
		if err == nil && len(tabs) > 0 {
			tab = tabs[0].ID
		}
	}
	if tab == "" {
		return map[string]any{"found": false}, nil
	}
	page, err := src.src.List(tab, 1)
	if err != nil {
		return nil, &clt01.ToolError{Code: "LOOKUP_FAILED", Message: "推荐列表没拿到"}
	}
	var pick video.Item
	for i := range page.Items {
		if page.Items[i].ID == "" || seen[page.Items[i].ID] {
			continue
		}
		pick = page.Items[i]
		break
	}
	if pick.ID == "" {
		return map[string]any{"found": false}, nil
	}
	v, err := src.src.Video(pick.ID)
	if err != nil || v == nil || len(v.Episodes) == 0 {
		return map[string]any{"found": false}, nil
	}
	item, ep, at := pick, v.Episodes[0], 0
	item.Source = src.id
	var st playState
	readOnMain(func() {
		a.cancelNext()
		a.playEpisode(item, ep, at)
		st = a.playState()
	})
	out := map[string]any{"found": true, "title": st.Title, "reason": "热门", "source": src.name}
	if st.Episode != "" {
		out["episode"] = st.Episode
	}
	_ = ctx
	return out, nil
}

// toolsPickPlay plays one video of the showing source: the hottest when hot is
// set, a random one otherwise. It is what "随机播放一个视频" and "播放最热视频"
// land on, whatever language they were said in.
func (a *app) toolsPickPlay(ctx context.Context, hot bool) (any, error) {
	var src *sourceEntry
	var tabID string
	readOnMain(func() {
		if a.sourceEntry != nil {
			s := *a.sourceEntry
			src = &s
		}
		tabID = a.tabID
	})
	if src == nil || !src.src.Can("videoList") {
		return map[string]any{"found": false}, nil
	}
	tab := tabID
	if hot {
		tab = ""
		tabs, err := src.src.Tabs()
		if err == nil {
			for _, t := range tabs {
				id := strings.ToLower(t.ID)
				if strings.Contains(id, "hot") || strings.Contains(id, "popular") || strings.Contains(id, "rank") {
					tab = t.ID
					break
				}
			}
			if tab == "" && len(tabs) > 0 {
				tab = tabs[0].ID
			}
		}
	}
	if tab == "" {
		return map[string]any{"found": false}, nil
	}
	page, err := src.src.List(tab, 1)
	if err != nil || page == nil || len(page.Items) == 0 {
		return map[string]any{"found": false}, nil
	}
	items := page.Items
	var pick video.Item
	if hot {
		pick = items[0]
	} else {
		pick = items[mrand.IntN(len(items))]
	}
	if pick.ID == "" {
		return map[string]any{"found": false}, nil
	}
	v, err := src.src.Video(pick.ID)
	if err != nil || v == nil || len(v.Episodes) == 0 {
		return map[string]any{"found": false}, nil
	}
	item, ep, at := pick, v.Episodes[0], 0
	item.Source = src.id
	var st playState
	readOnMain(func() {
		a.cancelNext()
		a.playEpisode(item, ep, at)
		st = a.playState()
	})
	out := map[string]any{"found": true, "title": st.Title, "source": src.name}
	if st.Episode != "" {
		out["episode"] = st.Episode
	}
	_ = ctx
	return out, nil
}
