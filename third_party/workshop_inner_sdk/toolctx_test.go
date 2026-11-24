package clt01

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// agent 头信息随 ctx 下发：handler 能取回会话/轮次/调用号。
func TestInvocationInContext(t *testing.T) {
	inv := Invocation{ID: "inv-1", SessionID: "s7", TurnID: "t3", RoundID: "r2",
		CallID: "c9", Name: "send_mail", Arguments: json.RawMessage(`{"to":"a@b.com"}`)}
	ctx := WithInvocation(context.Background(), inv)
	got, ok := InvocationFromContext(ctx)
	if !ok || got.CallID != "c9" || got.SessionID != "s7" || got.TurnID != "t3" {
		t.Fatalf("ctx 应取回 agent 头: %+v ok=%v", got, ok)
	}
	// 不在调用链上 → ok=false
	if _, ok := InvocationFromContext(context.Background()); ok {
		t.Fatal("空 ctx 不应取到 invocation")
	}
}

// ServeTools 全链路里 handler 拿到的 ctx 必须带 agent 头（端到端验证）。
func TestServeToolsHandlerContextCarriesAgentHeaders(t *testing.T) {
	fd := newFakeDevice(t, func(conn *websocket.Conn) {
		// 等 handler 记录完再下发第二条（避免竞争）——本测试只有一条 invoke
		time.Sleep(100 * time.Millisecond)
		payload, _ := json.Marshal(map[string]any{"type": "invoke", "data": map[string]any{
			"invocation_id": "inv-e2e", "integration_id": "i-echo", "revision": 1,
			"session_id": "sess-e2e", "turn_id": "turn-e2e", "round_id": "round-1",
			"call_id": "call-e2e", "name": "echo_meta", "version": "v1",
			"arguments": map[string]any{}, "timeout_ms": 5000,
		}})
		_ = conn.Write(context.Background(), websocket.MessageText, payload)
	})
	c := newClient(fd.httpServer())

	seen := make(chan Invocation, 1)
	ts, err := c.ServeTools(context.Background(), ToolServerOptions{
		IntegrationID: "i-echo", Enabled: true,
		Tools: []Tool{{Name: "echo_meta", Idempotent: true,
			Handle: func(ctx context.Context, args json.RawMessage) (any, error) {
				if inv, ok := InvocationFromContext(ctx); ok {
					seen <- inv
				}
				return map[string]any{"ok": true}, nil
			}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ts.Start(ctx)

	select {
	case inv := <-seen:
		if inv.SessionID != "sess-e2e" || inv.TurnID != "turn-e2e" || inv.CallID != "call-e2e" || inv.ID != "inv-e2e" {
			t.Fatalf("handler ctx 应携带 agent 头: %+v", inv)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("handler 未收到带 agent 头的调用")
	}
}
