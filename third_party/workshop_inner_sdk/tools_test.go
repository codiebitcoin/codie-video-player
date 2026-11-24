package clt01

// 外部工具集成（agent tools）端到端协议测试：
// fake 设备 = REST 目录（内存）+ WS（ready → 下发 invoke → 转发 SDK 回的 result）。
// 断言的是 SDK 发回设备的 result 形状 —— 这是我们唯一需要保证的协议契约。

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

type fakeDevice struct {
	srv        *httptest.Server
	mu         sync.Mutex
	catalog    map[string]json.RawMessage
	revision   map[string]int
	results    chan map[string]any // SDK 回的 result.data
	queries    chan string         // WS 握手 query
	wsAttempts chan string         // 每次握手尝试的 path（含 404）
	// legacyWS true = 契约定稿前的中间构建：只有旧路径 /v1/ws/tool-integrations/{id}，
	// 对 /executor 返回 404。默认 false（1.6.0 正式契约，只有 /executor）。
	legacyWS bool
}

// httpServer 返回 fake 设备的 HTTP 服务地址。
func (fd *fakeDevice) httpServer() *httptest.Server { return fd.srv }

// newFakeDevice 起一个最小 1.6.0 设备。onConnect(conn) 在 WS 建立后调用（设备侧主动下发 invoke 用）。
func newFakeDevice(t *testing.T, onConnect func(conn *websocket.Conn)) *fakeDevice {
	t.Helper()
	fd := &fakeDevice{
		catalog:    map[string]json.RawMessage{},
		revision:   map[string]int{},
		results:    make(chan map[string]any, 8),
		queries:    make(chan string, 8),
		wsAttempts: make(chan string, 8),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/api/data/user/tool-integrations", func(w http.ResponseWriter, r *http.Request) {
		fd.mu.Lock()
		defer fd.mu.Unlock()
		_, _ = w.Write([]byte(`{"success":true,"data":[]}`))
	})
	mux.HandleFunc("/v1/api/data/user/tool-integrations/", func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimPrefix(r.URL.Path, "/v1/api/data/user/tool-integrations/")
		fd.mu.Lock()
		defer fd.mu.Unlock()
		switch r.Method {
		case http.MethodGet:
			raw, ok := fd.catalog[id]
			if !ok {
				w.WriteHeader(404)
				_, _ = w.Write([]byte(`{"success":false,"code":"NOT_FOUND"}`))
				return
			}
			_, _ = w.Write(raw)
		case http.MethodPut:
			var body struct {
				ExpectedRevision int              `json:"expected_revision"`
				Name             string           `json:"name"`
				Enabled          bool             `json:"enabled"`
				Tools            []map[string]any `json:"tools"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body.ExpectedRevision != fd.revision[id] {
				w.WriteHeader(409)
				_, _ = w.Write([]byte(`{"success":false,"code":"REVISION_CONFLICT"}`))
				return
			}
			fd.revision[id]++
			obj, _ := json.Marshal(map[string]any{
				"id": id, "revision": fd.revision[id], "name": body.Name,
				"enabled": body.Enabled, "tools": body.Tools,
			})
			fd.catalog[id] = obj
			_, _ = w.Write([]byte(`{"success":true,"data":` + string(obj) + `}`))
		case http.MethodDelete:
			delete(fd.catalog, id)
			_, _ = w.Write([]byte(`{"success":true}`))
		}
	})
	mux.HandleFunc("/v1/ws/tool-integrations/", func(w http.ResponseWriter, r *http.Request) {
		fd.mu.Lock()
		legacyDevice := fd.legacyWS
		fd.mu.Unlock()
		rest := strings.TrimPrefix(r.URL.Path, "/v1/ws/tool-integrations/")
		// 新契约路径带 /executor 后缀；旧路径裸 id。
		if legacyDevice == strings.HasSuffix(rest, "/executor") {
			// 设备不认识这个代际的路径 → 路由不存在（gin 404）
			select {
			case fd.wsAttempts <- r.URL.Path:
			default:
			}
			w.WriteHeader(http.StatusNotFound)
			return
		}
		select {
		case fd.wsAttempts <- r.URL.Path:
		default:
		}
		fd.queries <- r.URL.RawQuery
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")
		id := strings.TrimSuffix(rest, "/executor")
		ready, _ := json.Marshal(map[string]any{"type": "ready", "data": map[string]any{
			"protocol_version": "v1", "integration_id": id, "revision": fd.revision[id]}})
		_ = conn.Write(r.Context(), websocket.MessageText, ready)
		if onConnect != nil {
			go onConnect(conn)
		}
		for {
			_, raw, err := conn.Read(r.Context())
			if err != nil {
				return
			}
			var msg struct {
				Type string         `json:"type"`
				Data map[string]any `json:"data"`
			}
			if json.Unmarshal(raw, &msg) != nil {
				continue
			}
			switch msg.Type {
			case "invoke":
			case "result":
				fd.results <- msg.Data
			}
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	fd.srv = srv
	return fd
}

// sendInvoke 设备侧下发一次工具调用。
func sendInvoke(conn *websocket.Conn, id, tool string, args map[string]any) {
	payload, _ := json.Marshal(map[string]any{"type": "invoke", "data": map[string]any{
		"invocation_id": id, "integration_id": "i-echo", "revision": 1,
		"name": tool, "version": "v1", "arguments": args,
		"timeout_ms": 5000,
	}})
	_ = conn.Write(context.Background(), websocket.MessageText, payload)
}

func newClient(srv *httptest.Server) *Client {
	c, _ := New(srv.URL)
	c.Apply(&Credentials{LocalSecret: "sec", UserID: "u1"})
	return c
}

func startTools(t *testing.T, c *Client, tools []Tool) *ToolServer {
	t.Helper()
	s, err := c.ServeTools(context.Background(), ToolServerOptions{
		IntegrationID: "i-echo", Name: "测试工具", Enabled: true, Tools: tools,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	s.Start(ctx)
	return s
}

func waitResult(t *testing.T) map[string]any {
	t.Helper()
	select {
	case r := <-(<-make(chan chan map[string]any)): // placeholder; never taken
		return r
	default:
	}
	return nil
}

// 成功路径：目录注册 → 握手 → invoke → result(succeeded, data)。
func TestServeToolsEcho(t *testing.T) {
	fd := newFakeDevice(t, func(conn *websocket.Conn) {
		// 等握手方的 ready 处理完成再下发（sleep 足够测试用）
		time.Sleep(100 * time.Millisecond)
		sendInvoke(conn, "call-1", "echo_text", map[string]any{"text": "你好"})
	})

	c := newClient(fd.httpServer())
	startTools(t, c, []Tool{{
		Name: "echo_text", Description: "回显",
		Parameters: []byte(`{"type":"object","properties":{"text":{"type":"string"}},"required":["text"],"additionalProperties":false}`),
		Risk:       "read", Idempotent: true,
		Handle: func(ctx context.Context, args json.RawMessage) (any, error) {
			var a struct {
				Text string `json:"text"`
			}
			_ = json.Unmarshal(args, &a)
			return map[string]any{"text": a.Text}, nil
		},
	}})

	// 握手 query 必须带签名参数
	select {
	case q := <-fd.queries:
		for _, want := range []string{"sig=", "timestamp=", "nonce=", "payload_mode=empty", "body_hash=", "user_id=u1"} {
			if !strings.Contains(q, want) {
				t.Errorf("握手 query 缺 %s: %s", want, q)
			}
		}
	case <-time.After(3 * time.Second):
		t.Fatal("未发起 WS 握手")
	}

	// 目录应已注册（PUT 成功过一次，revision=1）
	fd.mu.Lock()
	registered := len(fd.catalog) == 1 && fd.revision["i-echo"] == 1
	fd.mu.Unlock()
	if !registered {
		t.Fatal("目录未注册或 revision 未回写")
	}

	// 等 result
	deadline := time.After(5 * time.Second)
	for {
		select {
		case r := <-fd.results:
			if r["invocation_id"] != "call-1" {
				t.Fatalf("invocation_id 不对: %v", r["invocation_id"])
			}
			if r["status"] != "succeeded" {
				t.Fatalf("status 应为 succeeded: %v", r)
			}
			data, _ := r["data"].(map[string]any)
			if data == nil || data["text"] != "你好" {
				t.Fatalf("data 应含回显文本: %v", r["data"])
			}
			return
		case <-deadline:
			t.Fatal("未收到 result")
		}
	}
}

// 契约定稿前的旧构建：/executor 返回 404，SDK 应回退旧路径并正常完成调用。
func TestServeToolsLegacyWSFallback(t *testing.T) {
	fd := newFakeDevice(t, func(conn *websocket.Conn) {
		time.Sleep(100 * time.Millisecond)
		sendInvoke(conn, "call-legacy", "echo_text", map[string]any{"text": "旧路径"})
	})
	fd.mu.Lock()
	fd.legacyWS = true
	fd.mu.Unlock()

	c := newClient(fd.httpServer())
	startTools(t, c, []Tool{{
		Name: "echo_text", Idempotent: true,
		Handle: func(ctx context.Context, args json.RawMessage) (any, error) {
			var a struct {
				Text string `json:"text"`
			}
			_ = json.Unmarshal(args, &a)
			return map[string]any{"text": a.Text}, nil
		},
	}})

	// 第一次尝试应是新契约路径（404），随后回退旧路径
	select {
	case first := <-fd.wsAttempts:
		if first != "/v1/ws/tool-integrations/i-echo/executor" {
			t.Fatalf("应先试新契约路径: %s", first)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("未发起 WS 握手")
	}
	select {
	case second := <-fd.wsAttempts:
		if second != "/v1/ws/tool-integrations/i-echo" {
			t.Fatalf("404 后应回退旧路径: %s", second)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("未回退旧路径")
	}

	deadline := time.After(5 * time.Second)
	for {
		select {
		case r := <-fd.results:
			if r["invocation_id"] != "call-legacy" || r["status"] != "succeeded" {
				t.Fatalf("回退后应正常完成调用: %v", r)
			}
			return
		case <-deadline:
			t.Fatal("未收到 result")
		}
	}
}

// 正式契约设备（只认 /executor）：SDK 首次握手即用新路径，不碰旧路径。
func TestServeToolsExecutorPathPreferred(t *testing.T) {
	fd := newFakeDevice(t, nil)
	c := newClient(fd.httpServer())
	startTools(t, c, []Tool{{
		Name: "echo_text", Idempotent: true,
		Handle: func(ctx context.Context, args json.RawMessage) (any, error) { return nil, nil },
	}})
	select {
	case p := <-fd.wsAttempts:
		if p != "/v1/ws/tool-integrations/i-echo/executor" {
			t.Fatalf("1.6.0 正式契约应直接用 /executor: %s", p)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("未发起 WS 握手")
	}
}

// 执行失败：ToolError → failed + code；Clarify → needs_clarification。
func TestServeToolsFailureAndClarify(t *testing.T) {
	fd := newFakeDevice(t, func(conn *websocket.Conn) {
		time.Sleep(100 * time.Millisecond)
		sendInvoke(conn, "call-fail", "boom", map[string]any{})
		time.Sleep(200 * time.Millisecond)
		sendInvoke(conn, "call-clar", "ask_more", map[string]any{})
	})

	c := newClient(fd.httpServer())
	startTools(t, c, []Tool{
		{
			Name: "boom", Idempotent: true,
			Handle: func(ctx context.Context, args json.RawMessage) (any, error) {
				return nil, &ToolError{Code: "MAIL_REJECTED", Message: "smtp 5xx"}
			},
		},
		{
			Name: "ask_more", Idempotent: true,
			Handle: func(ctx context.Context, args json.RawMessage) (any, error) {
				return nil, Clarify("没听清收件人，请说完整邮箱")
			},
		},
	})

	// 第一条：failed + code
	r1 := <-fd.results
	if r1["invocation_id"] != "call-fail" || r1["status"] != "failed" || r1["code"] != "MAIL_REJECTED" {
		t.Fatalf("失败回包不对: %v", r1)
	}
	// 第二条：needs_clarification
	r2 := <-fd.results
	if r2["invocation_id"] != "call-clar" || r2["status"] != "needs_clarification" {
		t.Fatalf("追问回包不对: %v", r2)
	}
	if r2["message"] == "" {
		t.Error("追问应带提示文案")
	}
}

// 未知工具：SDK 侧直接回 UNKNOWN_TOOL，不调用任何 handler。
func TestServeToolsUnknownTool(t *testing.T) {
	fd := newFakeDevice(t, func(conn *websocket.Conn) {
		time.Sleep(100 * time.Millisecond)
		sendInvoke(conn, "call-x", "no_such_tool", map[string]any{})
	})
	c := newClient(fd.httpServer())
	startTools(t, c, []Tool{{
		Name: "echo_text", Idempotent: true,
		Handle: func(ctx context.Context, args json.RawMessage) (any, error) {
			t.Error("未知工具不应触发 handler")
			return nil, nil
		},
	}})
	r := <-fd.results
	if r["status"] != "failed" || r["code"] != "UNKNOWN_TOOL" {
		t.Fatalf("应回 UNKNOWN_TOOL: %v", r)
	}
}

// 目录 CRUD 原子方法：GET 404 → PUT → GET 回读 → DELETE。
func TestToolIntegrationCRUD(t *testing.T) {
	fd := newFakeDevice(t, nil)
	c := newClient(fd.httpServer())
	ctx := context.Background()

	// 未注册 → 404（APIError）
	if _, err := c.GetToolIntegration(ctx, "i-echo"); err == nil {
		t.Fatal("未注册时应 404")
	}
	raw, err := c.PutToolIntegration(ctx, "i-echo", ToolCatalogUpsert{
		ProtocolVersion: "v1", ExpectedRevision: 0, Name: "n", Enabled: true,
		Tools: []ToolSpec{{Name: "echo_text", Version: "v1", Idempotent: true, ConfirmationRequired: false}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var put struct {
		Revision int `json:"revision"`
	}
	_ = json.Unmarshal(raw, &put)
	if put.Revision != 1 {
		t.Fatalf("首次 PUT 后 revision 应为 1，got %d", put.Revision)
	}
	// 乐观并发：带旧 revision 再 PUT → 409
	if _, err := c.PutToolIntegration(ctx, "i-echo", ToolCatalogUpsert{
		ProtocolVersion: "v1", ExpectedRevision: 0, Name: "n", Enabled: true,
		Tools: []ToolSpec{{Name: "echo_text", Version: "v1", Idempotent: true, ConfirmationRequired: false}},
	}); err == nil {
		t.Fatal("过期 revision 应 409")
	}
	if _, err := c.DeleteToolIntegration(ctx, "i-echo"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.GetToolIntegration(ctx, "i-echo"); err == nil {
		t.Fatal("删除后应 404")
	}
}
