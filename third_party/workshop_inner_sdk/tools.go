package clt01

// 外部工具集成（agent tools）的调用通道：设备把「模型选中的工具调用」经专用 WS 推给宿主，
// 宿主执行后回结果，设备侧 LLM 用结果续写对话。
//
// 协议（对齐固件 1.6.0 externaltools/manager.go 与官方示例 external_tools.py）：
//
//	握手：GET /v1/ws/tool-integrations/{integration_id}/executor?sig=…×tamp=…&nonce=…
//	      &payload_mode=empty&body_hash=…&user_id=…[&session_id=…]
//	      （签名与 HTTP 相同，签实际请求 path 且不含 query；1.6.0 契约定稿 f74741bd5 起带
//	       /executor 后缀，定稿前的中间构建仍在 /v1/ws/tool-integrations/{id}，握手 404 时自动回退）
//	设备 → {"type":"ready","data":{"protocol_version":"v1","integration_id":…,"revision":N}}
//	设备 → {"type":"invoke","data":{"invocation_id","integration_id","revision","session_id",
//	                              "turn_id","round_id","call_id","name","version",
//	                              "arguments":{…},"timeout_ms":N,"deadline_unix_ms":N}}
//	宿主 → {"type":"result","data":{"invocation_id","status":"succeeded|failed|needs_clarification",
//	                              "data":{…}          // 须符合 result_schema，≤16KiB
//	                              "code","message"}}  // failed 必须带 code（≤64 字符）
//	设备 → {"type":"error","data":{"invocation_id"?, "code":"…"}}（协议级错误）
//
// 服务端 20s 一次 ping；invoke 带 deadline，过期调用设备自己会以 EXTERNAL_TOOL_DEADLINE 失败。
//
// 能力门槛（再强调一次）：调用资格只跟「当前会话冻结时的推理模型」有关——
// only user-defined AI providers actually select these tools; the built-in inference model never calls them.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// StableIntegrationID 由任意名称生成一个**稳定的 RFC 4122 v5 UUID**（SHA1，零依赖）。
//
// 设备端要求集成 ID 是合法 UUID（uuid.Parse + 规范化相等校验），任意自由字符串都会被
// 400 拒绝；同时 ID 必须跨重启稳定（同一 ID 的 PUT 是「更新目录」，新 ID 是「重复注册」）。
// v5 UUID 两个性质都满足：同一输入永远得到同一个 UUID。
func StableIntegrationID(name string) string {
	// RFC 4122 §4.3：v5 = sha1(namespace_bytes + name)，取 16 字节，改版本/变体位。
	const nsDNS = "6ba7b810-9dad-11d1-80b4-00c04fd430c8" // 标准命名空间（DNS）
	var ns [16]byte
	_, _ = hex.Decode(ns[:], []byte(strings.ReplaceAll(nsDNS, "-", "")))
	h := sha256.New() // sha1 在部分环境被禁用；这里 v5 语义换成 sha256 取前 16 字节
	h.Write(ns[:])
	h.Write([]byte(name))
	var u [16]byte
	copy(u[:], h.Sum(nil)[:16])
	u[6] = (u[6] & 0x0f) | 0x50 // version 5
	u[8] = (u[8] & 0x3f) | 0x80 // variant RFC4122
	return fmt.Sprintf("%x-%x-%x-%x-%x", u[0:4], u[4:6], u[6:8], u[8:10], u[10:16])
}

// 工具调用结果状态（设备端 devicetools.Status 的三个合法值）。
const (
	ToolStatusSucceeded          = "succeeded"
	ToolStatusFailed             = "failed"
	ToolStatusNeedsClarification = "needs_clarification"
)

// ToolError 宿主执行失败时回给设备的状态与错误码（code ≤64 字符）。
type ToolError struct {
	Code    string // 如 "MAIL_REJECTED"，设备侧包装为 EXTERNAL_TOOL_FAILED
	Message string
}

func (e *ToolError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return e.Code
}

// Clarify 返回 needs_clarification 结果：设备会把提示交给模型向用户追问，
// 而不是报错（例如「没听清收件人，请说完整邮箱」）。
func Clarify(message string) *ToolError {
	return &ToolError{Code: "NEEDS_CLARIFICATION", Message: message}
}

// Tool 一个可被设备 agent 调用的工具：声明（发给设备目录）+ 执行体（宿主本地）。
type Tool struct {
	Name                 string `json:"name"` // ^[a-z][a-z0-9_]{0,23}$
	Version              string `json:"version,omitempty"`
	Description          string `json:"description,omitempty"`
	Parameters           []byte `json:"parameters,omitempty"` // JSON Schema
	ResultSchema         []byte `json:"result_schema,omitempty"`
	Risk                 string `json:"risk,omitempty"` // read | reversible | consequential
	Idempotent           bool   `json:"idempotent"`
	ConfirmationRequired bool   `json:"confirmation_required"`
	TimeoutMS            int    `json:"timeout_ms,omitempty"` // 100–20000，0 = 设备默认 5000

	// Handle 执行一次调用。返回的 result 会被 JSON 序列化进 data；
	// 返回 *ToolError 时按 failed（或 Clarify → needs_clarification）上报。
	Handle func(ctx context.Context, args json.RawMessage) (any, error)
}

// ToolServerOptions 一个工具集成（一个 integration = 一条 WS 连接）。
type ToolServerOptions struct {
	IntegrationID string // 稳定的小写 UUID，宿主自选；重启保持不变以便更新目录
	Name          string // 目录显示名
	Enabled       bool
	Tools         []Tool

	// Register 控制是否在连接前 PUT 目录（默认 true）。目录用 expected_revision
	// 做乐观并发：先 GET 现状（404 = revision 0），失败报错不连接。
	Register *bool

	// OnInvoke / OnResult 仅做观察（日志/审计），返回 error 不影响协议。
	OnInvoke func(inv Invocation)
	OnResult func(inv Invocation, status string, err error)

	// Reconnect 断线重连等待；零值 = 2s。
	Reconnect time.Duration
}

// Invocation 一次工具调用（invoke 消息的 data）。
type Invocation struct {
	ID          string          `json:"invocation_id"`
	Integration string          `json:"integration_id"`
	Revision    int             `json:"revision"`
	SessionID   string          `json:"session_id"`
	TurnID      string          `json:"turn_id"`
	RoundID     string          `json:"round_id"`
	CallID      string          `json:"call_id"`
	Name        string          `json:"name"`
	Version     string          `json:"version"`
	Arguments   json.RawMessage `json:"arguments"`
	TimeoutMS   int64           `json:"timeout_ms"`
	DeadlineMS  int64           `json:"deadline_unix_ms"`
}

// ToolServer 一个运行中的工具服务。Start 阻塞（内部含重连），Stop 停止。
type ToolServer struct {
	c   *Client
	opt ToolServerOptions

	mu    sync.Mutex
	ready bool
	// legacyPath 已探明设备只认旧握手路径（契约定稿前的 1.6.0 中间构建）；
	// 重连时优先用它，握手 404 再切回新路径（设备升级后自愈）。
	legacyPath bool
}

// ToolServerAvailable 判断该设备固件是否具备外部工具集成能力（≥1.6.0）。
// 只查固件版本阈值；「模型会不会真的调用」由设备侧按当前推理模型决定，这里无法预知。
// nil 客户端（未连接/测试场景）= 不可用。
func (c *Client) ToolServerAvailable() bool {
	if c == nil {
		return false
	}
	p, err := c.Firmware()
	if err != nil || p == nil {
		return false
	}
	return p.Version.AtLeast(VerToolIntegrations)
}

// ServeTools 注册目录并开始服务调用（阻塞；内部自动重连直到 ctx 取消）。
//
// 用法：ctx 取消即停止；单连接内并发执行调用（设备端允许一次多 invoke）。
func (c *Client) ServeTools(ctx context.Context, opt ToolServerOptions) (*ToolServer, error) {
	if strings.TrimSpace(opt.IntegrationID) == "" {
		return nil, fmt.Errorf("clt01: ServeTools 需要 IntegrationID（稳定的小写 UUID）")
	}
	if len(opt.Tools) == 0 {
		return nil, fmt.Errorf("clt01: ServeTools 至少需要一个工具")
	}
	if opt.Reconnect <= 0 {
		opt.Reconnect = 2 * time.Second
	}
	s := &ToolServer{c: c, opt: opt}
	if err := s.registerCatalog(ctx); err != nil {
		return nil, err
	}
	return s, nil
}

// Start 后台跑 ServeTools 循环。
func (s *ToolServer) Start(ctx context.Context) {
	go func() {
		backoff := s.opt.Reconnect
		for {
			if err := s.serveOnce(ctx); err != nil && ctx.Err() == nil {
				fmt.Printf("clt01: tool server %s: %v（%v 后重连）\n", s.opt.IntegrationID, err, backoff)
				select {
				case <-time.After(backoff):
				case <-ctx.Done():
					return
				}
			}
			if ctx.Err() != nil {
				return
			}
		}
	}()
}

// registerCatalog 按 expected_revision 乐观并发注册/更新目录。
func (s *ToolServer) registerCatalog(ctx context.Context) error {
	if s.opt.Register != nil && !*s.opt.Register {
		return nil
	}
	revision := 0
	raw, err := s.c.GetToolIntegration(ctx, s.opt.IntegrationID)
	if err == nil {
		var cur struct {
			Revision int `json:"revision"`
		}
		if json.Unmarshal(raw, &cur) == nil {
			revision = cur.Revision
		}
	} // 404（未注册过）→ revision 保持 0
	if err := s.putCatalog(ctx, revision); err == nil {
		return nil
	} else if ae, ok := err.(*APIError); !ok || ae.StatusCode != http.StatusConflict {
		return err
	}
	// 409 TOOL_VERSION_CONFLICT：设备把「集成内 同名同版本 不同内容」判为冲突（改 schema 必须
	// 升 Version）。宿主改了工具内容却忘了升版本时，删掉整个集成重建（revision 归零）自愈。
	_, _ = s.c.DeleteToolIntegration(ctx, s.opt.IntegrationID)
	return s.putCatalog(ctx, 0)
}

// putCatalog 组目录载荷并 PUT（expected_revision 乐观并发）。
func (s *ToolServer) putCatalog(ctx context.Context, revision int) error {
	tools := make([]ToolSpec, 0, len(s.opt.Tools))
	for _, t := range s.opt.Tools {
		tools = append(tools, ToolSpec{
			Name: t.Name, Version: orDefault(t.Version, "v1"), Description: t.Description,
			Parameters: t.Parameters, ResultSchema: t.ResultSchema, Risk: t.Risk,
			Idempotent:           t.Idempotent,
			ConfirmationRequired: t.ConfirmationRequired,
			TimeoutMS:            t.TimeoutMS,
		})
	}
	catalog := ToolCatalogUpsert{
		ProtocolVersion: "v1", ExpectedRevision: revision,
		Name: orDefault(s.opt.Name, s.opt.IntegrationID), Enabled: s.opt.Enabled,
		Tools: tools,
	}
	_, err := s.c.PutToolIntegration(ctx, s.opt.IntegrationID, catalog)
	return err
}

// serveOnce 连一次 WS 并服务到出错/关闭。
//
// 握手路径两代并存：1.6.0 正式契约为 /v1/ws/tool-integrations/{id}/executor，
// 定稿前的中间构建只有 /v1/ws/tool-integrations/{id}。优先按已探明的代际连接；
// 未探明时先试新路径，握手 404（路由不存在）再落回旧路径并记住。
func (s *ToolServer) serveOnce(ctx context.Context) error {
	current := "/v1/ws/tool-integrations/" + s.opt.IntegrationID + "/executor"
	legacy := "/v1/ws/tool-integrations/" + s.opt.IntegrationID
	paths := []string{current, legacy}
	s.mu.Lock()
	if s.legacyPath {
		paths[0], paths[1] = paths[1], paths[0]
	}
	s.mu.Unlock()

	var lastErr error
	for _, path := range paths {
		conn, err := s.dialToolWS(ctx, path)
		if err != nil {
			lastErr = fmt.Errorf("%s: %w", path, err)
			// 仅路由不存在（404）时才换代际重试；401 验签失败等对两个路径结果相同
			he, ok := err.(*wsHandshakeError)
			if !ok || he.status != http.StatusNotFound {
				return lastErr
			}
			continue
		}
		s.mu.Lock()
		s.legacyPath = path == legacy
		s.mu.Unlock()
		return s.serveConn(ctx, conn)
	}
	return lastErr
}

// dialToolWS 对指定路径做签名握手，失败时带回 HTTP 状态（resp 非 nil）。
func (s *ToolServer) dialToolWS(ctx context.Context, path string) (*websocket.Conn, error) {
	wsURL, err := s.c.toolWSURLFor(path)
	if err != nil {
		return nil, err
	}
	// coder/websocket 会带上标准升级头；鉴权信息全在 query（与官方示例一致）
	conn, resp, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		if resp != nil {
			defer resp.Body.Close()
			return nil, &wsHandshakeError{status: resp.StatusCode}
		}
		return nil, err
	}
	return conn, nil
}

// wsHandshakeError 握手被服务端以 HTTP 状态拒绝（非 101）。
type wsHandshakeError struct {
	status int
}

func (e *wsHandshakeError) Error() string { return fmt.Sprintf("WS 握手 HTTP %d", e.status) }

// serveConn 在已建立的连接上读消息并分发（读循环；调用 goroutine 执行）。
func (s *ToolServer) serveConn(ctx context.Context, conn *websocket.Conn) error {
	defer conn.Close(websocket.StatusNormalClosure, "")

	conn.SetReadLimit(1 << 20)
	// 读循环：单 goroutine 读；调用 goroutine 执行；coder/websocket 允许一个读者 + 并发写
	for {
		_, raw, err := conn.Read(ctx)
		if err != nil {
			return err
		}
		var msg struct {
			Type string          `json:"type"`
			Data json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(raw, &msg); err != nil {
			continue
		}
		switch msg.Type {
		case "ready":
			var d struct {
				ProtocolVersion string `json:"protocol_version"`
				IntegrationID   string `json:"integration_id"`
				Revision        int    `json:"revision"`
			}
			_ = json.Unmarshal(msg.Data, &d)
			s.mu.Lock()
			s.ready = true
			s.mu.Unlock()
			fmt.Printf("clt01: tool server %s online（revision %d）\n", d.IntegrationID, d.Revision)
		case "invoke":
			var inv Invocation
			if err := json.Unmarshal(msg.Data, &inv); err != nil {
				continue
			}
			go s.handleInvoke(ctx, conn, inv)
		case "error":
			var d struct {
				InvocationID string `json:"invocation_id"`
				Code         string `json:"code"`
			}
			_ = json.Unmarshal(msg.Data, &d)
			fmt.Printf("clt01: tool server %s 协议错误 %s（invocation %s）\n", s.opt.IntegrationID, d.Code, d.InvocationID)
		}
	}
}

// handleInvoke 找工具、带超时执行、回 result。
func (s *ToolServer) handleInvoke(ctx context.Context, conn *websocket.Conn, inv Invocation) {
	if s.opt.OnInvoke != nil {
		s.opt.OnInvoke(inv)
	}
	var tool *Tool
	for i := range s.opt.Tools {
		if s.opt.Tools[i].Name == inv.Name {
			tool = &s.opt.Tools[i]
			break
		}
	}
	if tool == nil || tool.Handle == nil {
		s.report(conn, inv, "", &ToolError{Code: "UNKNOWN_TOOL", Message: "unknown tool " + inv.Name})
		return
	}
	// 超时：取设备 deadline 与本地剩余时间的较小者
	callCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	if inv.DeadlineMS > 0 {
		if d := time.UnixMilli(inv.DeadlineMS); d.After(time.Now()) {
			tctx, tcancel := context.WithDeadline(ctx, d)
			defer tcancel()
			callCtx = tctx
		}
	} else if inv.TimeoutMS > 0 {
		tctx, tcancel := context.WithTimeout(ctx, time.Duration(inv.TimeoutMS)*time.Millisecond)
		defer tcancel()
		callCtx = tctx
	}
	// agent 头信息随 ctx 下发：handler（及其下游业务层）可取会话/轮次/调用号做审计与去重
	result, err := tool.Handle(WithInvocation(callCtx, inv), inv.Arguments)
	if s.opt.OnResult != nil {
		s.opt.OnResult(inv, statusOf(err), err)
	}
	s.report(conn, inv, result, asToolErr(err))
}

// report 回一条 result（succeeded / failed / needs_clarification）。
func (s *ToolServer) report(conn *websocket.Conn, inv Invocation, result any, terr *ToolError) {
	data := map[string]any{"invocation_id": inv.ID}
	switch {
	case terr == nil:
		data["status"] = ToolStatusSucceeded
		if result != nil {
			data["data"] = result
		} else {
			data["data"] = map[string]any{}
		}
	case terr.Code == "NEEDS_CLARIFICATION":
		data["status"] = ToolStatusNeedsClarification
		data["message"] = terr.Message
		data["data"] = map[string]any{}
	default:
		data["status"] = ToolStatusFailed
		data["code"] = clampCode(terr.Code)
		data["message"] = terr.Message
		data["data"] = map[string]any{}
	}
	payload, _ := json.Marshal(map[string]any{"type": "result", "data": data})
	wctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = conn.Write(wctx, websocket.MessageText, payload)
}

// toolWSURL 组带签名 query 的 WS 地址（签名路径不含 query）。
func (c *Client) toolWSURLFor(path string) (string, error) {
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	nonce, err := randomNonce()
	if err != nil {
		return "", err
	}
	bodyHash := sha256.Sum256(nil)
	sig := Sign(c.Secret, http.MethodGet, path, ts, nonce, string(PayloadModeEmpty), hex.EncodeToString(bodyHash[:]), c.UserID)
	q := fmt.Sprintf("sig=%s&timestamp=%s&nonce=%s&payload_mode=empty&body_hash=%s&user_id=%s",
		sig, ts, nonce, hex.EncodeToString(bodyHash[:]), c.UserID)
	if c.SessionID != "" {
		q += "&session_id=" + c.SessionID
	}
	base := strings.Replace(c.BaseURL, "https://", "wss://", 1)
	base = strings.Replace(base, "http://", "ws://", 1)
	return base + path + "?" + q, nil
}

func asToolErr(err error) *ToolError {
	if err == nil {
		return nil
	}
	if te, ok := err.(*ToolError); ok {
		return te
	}
	return &ToolError{Code: "TOOL_ERROR", Message: truncateStr(err.Error(), 200)}
}

func statusOf(err error) string {
	if err == nil {
		return ToolStatusSucceeded
	}
	if te, ok := err.(*ToolError); ok && te.Code == "NEEDS_CLARIFICATION" {
		return ToolStatusNeedsClarification
	}
	return ToolStatusFailed
}

func clampCode(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "TOOL_ERROR"
	}
	if len(s) > 64 {
		s = s[:64]
	}
	return s
}

func truncateStr(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

func orDefault(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}
