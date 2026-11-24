package clt01

// 1.6.0 原子接口（按固件 docs/api/openapi.json@1.6.0）：
//   - 外部工具集成（agent tools）目录 CRUD：/data/user/tool-integrations
//   - 角色模型体型/形态参数：/data/user/character/model(+ /control)
//
// 调用通道 WS（1.6.0 契约为 /v1/ws/tool-integrations/{id}/executor，定稿前中间构建为
// /v1/ws/tool-integrations/{id}）的高层封装在 tools.go。
//
// 手册要点（1.6.0 openapi 原文摘要）：
//   - 外部工具的选择、调用与结果续写**仅向用户自定义模型（AI Provider）开放**；
//     not available when the actual inference model is the device's built-in one -- the catalog may pre-register, but "registered / WS ready"
//     只代表目录和连接就绪，不代表模型具备调用资格；资格在新会话冻结时确定。
//   - PUT 目录为整体替换 + expected_revision 乐观并发；body 上限 128KiB；
//     tools[].idempotent 与 confirmation_required 必须显式提交（防确认策略被静默降级）。

import (
	"context"
	"encoding/json"
	"net/http"
)

// ---- 外部工具集成（agent tools） ----

// ToolIntegration 一个已注册的外部工具集成目录。
type ToolIntegration struct {
	ID              string          `json:"id"`
	Revision        int             `json:"revision"`
	Name            string          `json:"name"`
	Enabled         bool            `json:"enabled"`
	ProtocolVersion string          `json:"protocol_version"`
	Tools           []ToolSpec      `json:"tools"`
	Extra           json.RawMessage `json:"-"` // 未知字段保留（设备可能携带状态，如 WS 在线标记）
}

// ToolSpec 目录里的一个工具声明。
type ToolSpec struct {
	Name                 string          `json:"name"`     // ^[a-z][a-z0-9_]{0,23}$
	Version              string          `json:"version"`  // 如 "v1"
	Description          string          `json:"description,omitempty"`
	Parameters           json.RawMessage `json:"parameters,omitempty"` // JSON Schema（object 根，深度≤6，属性≤64，≤8KiB）
	ResultSchema         json.RawMessage `json:"result_schema,omitempty"`
	Risk                 string          `json:"risk,omitempty"` // read | reversible | consequential
	Idempotent           bool            `json:"idempotent"`     // 必填（缺省会被设备 400 拒绝）
	ConfirmationRequired bool            `json:"confirmation_required"` // 必填
	TimeoutMS            int             `json:"timeout_ms,omitempty"`  // 100–20000，缺省 5000
}

// ToolCatalogUpsert PUT /tool-integrations/{id} 的请求体。
// 全部字段必填（设备端 DisallowUnknownFields + 逐字段非空校验）。
type ToolCatalogUpsert struct {
	ProtocolVersion string    `json:"protocol_version"` // 恒为 "v1"
	ExpectedRevision int      `json:"expected_revision"` // 乐观并发：当前 revision；新建填 0
	Name            string    `json:"name"`
	Enabled         bool      `json:"enabled"`
	Tools           []ToolSpec `json:"tools"`
}

// ListToolIntegrations 列出外部工具集成。
func (c *Client) ListToolIntegrations(ctx context.Context) (json.RawMessage, error) {
	return c.get153(ctx, "/v1/api/data/user/tool-integrations", nil)
}

// GetToolIntegration 读取一个外部工具集成。
func (c *Client) GetToolIntegration(ctx context.Context, integrationID string) (json.RawMessage, error) {
	return c.get153(ctx, "/v1/api/data/user/tool-integrations/"+integrationID, nil)
}

// PutToolIntegration 注册或更新一个外部工具集成（整体替换 + 乐观并发）。
// 返回设备回写的对象（含新 revision）。
func (c *Client) PutToolIntegration(ctx context.Context, integrationID string, catalog ToolCatalogUpsert) (json.RawMessage, error) {
	return c.do153(ctx, http.MethodPut, "/v1/api/data/user/tool-integrations/"+integrationID, catalog)
}

// DeleteToolIntegration 删除一个外部工具集成。
func (c *Client) DeleteToolIntegration(ctx context.Context, integrationID string) (json.RawMessage, error) {
	return c.do153(ctx, http.MethodDelete, "/v1/api/data/user/tool-integrations/"+integrationID, nil)
}

// ---- 角色模型体型/形态（1.6.0 新增） ----

// CharacterModelControl POST /data/user/character/model/control 的动作体。
// 三种 target：height（0.7–2.4 米）、morph（value 0–1 + ref）、或 reset/reset_category/reset_all。
type CharacterModelControl struct {
	Action   string          `json:"action"`            // preview | commit | reset | reset_category | reset_all
	Target   string          `json:"target"`            // height | morph
	Value    float64         `json:"value,omitempty"`   // height/morph 用
	Ref      json.RawMessage `json:"ref,omitempty"`     // morph 用
	Semantic int             `json:"semantic,omitempty"` // reset_category 用（0–5）
}

// CharacterModel 读取当前模型身高与形态参数。
func (c *Client) CharacterModel(ctx context.Context) (json.RawMessage, error) {
	return c.get153(ctx, "/v1/api/data/user/character/model", nil)
}

// CharacterModelControl 预览、保存或重置当前模型参数。
func (c *Client) CharacterModelControl(ctx context.Context, in CharacterModelControl) (json.RawMessage, error) {
	return c.do153(ctx, http.MethodPost, "/v1/api/data/user/character/model/control", in)
}

// ---- AI Provider 配置（设备侧自定义模型；宿主「设置自己的 API」用） ----
//
// 这些端点在固件源码（user_routes.go /ai 组）而不在公开契约里，字段形状按
// handlers_gin/web/ai_provider_gin.go 锚定。用途：让设备用用户自己的 LLM API
// —— 这是 agent tools 调用资格的前提之一（backend=local_harness + 路由指向 profile）。
//
// 激活顺序：CreateAIProviderProfile → TestAIProviderProfile（probe.tools=available）
// → UpdateCapabilityRoute(pipeline_llm/llm) → SwitchAIBackend("local_harness")。

// ListAIProviderCatalog 设备支持的 provider 目录（id/base_url/能力/默认格式）。
func (c *Client) ListAIProviderCatalog(ctx context.Context) (json.RawMessage, error) {
	return c.get153(ctx, "/v1/api/data/user/ai/providers/catalog", nil)
}

// ListAIProviderProfiles 已建的 provider profile 列表。
func (c *Client) ListAIProviderProfiles(ctx context.Context) (json.RawMessage, error) {
	return c.get153(ctx, "/v1/api/data/user/ai/provider-profiles", nil)
}

// AIProviderProfileInput 建/改 profile 的字段（api_key 建时必带；凭据由设备托管）。
type AIProviderProfileInput struct {
	ProviderID   string   `json:"provider_id"`   // catalog 里的 id（如 deepseek / custom_openai_compatible）
	DisplayName  string   `json:"display_name"`
	Capabilities []string `json:"capabilities"`  // llm / vlm / tts / asr / realtime_conversation / live_conversation
	Enabled      bool     `json:"enabled"`
	BaseURL      string   `json:"base_url,omitempty"` // 仅 editable_base_url 的 provider 有效
	APIKey       string   `json:"api_key,omitempty"`
}

// CreateAIProviderProfile 创建一个 provider profile（返回含 id）。
func (c *Client) CreateAIProviderProfile(ctx context.Context, in AIProviderProfileInput) (json.RawMessage, error) {
	return c.do153(ctx, http.MethodPost, "/v1/api/data/user/ai/provider-profiles", in)
}

// UpdateAIProviderProfile 更新一个 profile（字段形状同创建；空字段保持原值）。
func (c *Client) UpdateAIProviderProfile(ctx context.Context, id string, in AIProviderProfileInput) (json.RawMessage, error) {
	return c.do153(ctx, http.MethodPut, "/v1/api/data/user/ai/provider-profiles/"+id, in)
}

// DeleteAIProviderProfile 删除一个 profile。
func (c *Client) DeleteAIProviderProfile(ctx context.Context, id string) (json.RawMessage, error) {
	return c.do153(ctx, http.MethodDelete, "/v1/api/data/user/ai/provider-profiles/"+id, nil)
}

// TestAIProviderProfile 连通性测试。响应的 probe.capabilities 里 tools:"available"
// 即该模型具备外部工具调用资格（这是设备侧自己报的，宿主不用猜）。
func (c *Client) TestAIProviderProfile(ctx context.Context, id string) (json.RawMessage, error) {
	return c.do153(ctx, http.MethodPost, "/v1/api/data/user/ai/provider-profiles/"+id+"/test", map[string]any{})
}

// ListCapabilityRoutes 当前能力路由（pipeline_llm/llm/… 指向 company_cloud 还是 provider）。
func (c *Client) ListCapabilityRoutes(ctx context.Context) (json.RawMessage, error) {
	return c.get153(ctx, "/v1/api/data/user/ai/capability-routes", nil)
}

// CapabilityRouteInput 一条路由的目标。
type CapabilityRouteInput struct {
	TargetType string `json:"target_type"`              // provider | company_cloud | conversation | reasoning_backend
	ProfileID  string `json:"profile_id,omitempty"`     // target_type=provider 时必填
	APIFormat  string `json:"api_format,omitempty"`     // 如 openai_chat_completions
	Model      string `json:"model,omitempty"`          // 如 deepseek-flash
	Voice      string `json:"voice,omitempty"`
}

// UpdateCapabilityRoute 把一条能力路由指到目标。对话 LLM 用 pipeline_llm（chained 架构）。
func (c *Client) UpdateCapabilityRoute(ctx context.Context, capability string, in CapabilityRouteInput) (json.RawMessage, error) {
	return c.do153(ctx, http.MethodPut, "/v1/api/data/user/ai/capability-routes/"+capability, in)
}

// GetAIBackend 当前对话后端与可切换性（backend/architecture/local_harness_ready）。
func (c *Client) GetAIBackend(ctx context.Context) (json.RawMessage, error) {
	return c.get153(ctx, "/v1/api/data/user/ai/backend", nil)
}

// SwitchAIBackend 切对话后端。
// **cloud/bridge 不接受外部工具声明**（固件注释原话）；要注入 agent tools 必须 local_harness。
func (c *Client) SwitchAIBackend(ctx context.Context, backend string) (json.RawMessage, error) {
	return c.do153(ctx, http.MethodPost, "/v1/api/data/user/ai/backend/switch", map[string]string{"backend": backend})
}

// GetAISystemPrompt / UpdateAISystemPrompt 对话 system prompt（人设拼装在设备侧）。
func (c *Client) GetAISystemPrompt(ctx context.Context) (json.RawMessage, error) {
	return c.get153(ctx, "/v1/api/data/user/ai/system-prompt", nil)
}

func (c *Client) UpdateAISystemPrompt(ctx context.Context, prompt string) (json.RawMessage, error) {
	return c.do153(ctx, http.MethodPut, "/v1/api/data/user/ai/system-prompt", map[string]any{"system_prompt": prompt})
}
