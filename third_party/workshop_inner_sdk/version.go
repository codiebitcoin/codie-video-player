package clt01

import (
	"net/http"
	"strconv"
	"strings"
	"sync"
)

// 本文件实现固件版本模型：版本号用字符串（"1.5.2"、"1.5.3"、"1.6.0"，可带 v 前缀），
// 大小比较用数值化的字符串比较（CompareVersions）—— 未来 v1.5.4 / v1.6.x 等新版本
// 无需改任何枚举，只需按能力阈值（VerAsyncUpload / VerChunkedUpload…）分发。
//
// 能力分层（capability tiers，按阈值而非精确版本判断）：
//   - < 1.5.0                : legacy —— 仅 job/file；complete 可能不存在
//   - ≥ 1.5.0 (asyncUpload)  : async multipart 上传 + PUT 三件套 model-apply
//   - ≥ 1.5.3 (chunkedUpload): gzip/zstd 压缩分块通道 + 8MiB stream 限制
//
// 设备不暴露版本号，版本按只读能力探测（uploaded-models）确定基线；
// 1.5.2 与 1.5.3 的只读接口相同，分块通道在首次上传时试探并回写缓存。

// FirmwareVersion 固件版本号（字符串形式，如 "1.5.2"；比较用 AtLeast/CompareVersions）。
type FirmwareVersion string

// 能力阈值版本（ver.AtLeast(threshold) 即具备该能力层）。
const (
	VerChunkedUpload FirmwareVersion = "1.5.3" // 压缩分块通道（≥1.5.3）
	VerAsyncUpload   FirmwareVersion = "1.5.0" // async 上传 + model-apply（≥1.5.0）
	// VerToolIntegrations 外部工具集成（agent tools，≥1.6.0）：
	// REST 目录注册 + 专用 WS 调用通道（1.6.0 契约为 /v1/ws/tool-integrations/{id}/executor，
	// 定稿前中间构建为 /v1/ws/tool-integrations/{id}，tools.go 握手时自动探测）。
	// 注册成功只代表目录与连接就绪；模型是否真的调用取决于当前推理模型
	//（only available for user-defined AI providers; the device's built-in inference models do not expose external tools）。
	VerToolIntegrations FirmwareVersion = "1.6.0"

	// 各能力层的代表版本（探测不到精确版本时的占位）。
	VerLegacy FirmwareVersion = "1.4.0" // < 1.5.0
	VerV15x   FirmwareVersion = "1.5.2" // 1.5.0 ~ 1.5.2 基线
	VerV153   FirmwareVersion = "1.5.3" // ≥ 1.5.3
	VerV160   FirmwareVersion = "1.6.0" // ≥ 1.6.0
)

// CompareVersions 数值化字符串版本比较：逐段按整数比较，缺省段按 0，
// 因此 "1.5.10" > "1.5.9"、"1.6" > "1.5.9"、"v1.5.3" == "1.5.3"。
// 返回 -1/0/1。无法解析为数字的段按字符串比较兜底。
func CompareVersions(a, b FirmwareVersion) int {
	as, bs := splitVersion(a), splitVersion(b)
	for i := 0; i < len(as) || i < len(bs); i++ {
		if i >= len(as) {
			if zeroRest(bs[i:]) {
				return 0
			}
			return -1
		}
		if i >= len(bs) {
			if zeroRest(as[i:]) {
				return 0
			}
			return 1
		}
		an, aok := parseNum(as[i])
		bn, bok := parseNum(bs[i])
		switch {
		case aok && bok:
			if an != bn {
				if an < bn {
					return -1
				}
				return 1
			}
		case aok != bok: // 一方能解析另一方不能：数字段 < 非数字段（如 3 < 4-rc 的 "4-rc"）
			if aok {
				return 1
			}
			return -1
		default:
			if c := strings.Compare(as[i], bs[i]); c != 0 {
				return c
			}
		}
	}
	return 0
}

// zeroRest 剩余段是否全为 0（"1.5" vs "1.5.0.0" 视为相等）。
func zeroRest(segs []string) bool {
	for _, s := range segs {
		if n, ok := parseNum(s); !ok || n != 0 {
			return false
		}
	}
	return true
}

// splitVersion 归一化版本串：去空白与 v/V 前缀，去 "-" 预发布后缀，按 "." 切分。
func splitVersion(v FirmwareVersion) []string {
	s := strings.TrimSpace(string(v))
	s = strings.TrimLeft(s, "vV")
	if i := strings.IndexByte(s, '-'); i >= 0 {
		s = s[:i]
	}
	return strings.Split(s, ".")
}

func parseNum(s string) (int, bool) {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	return n, err == nil
}

// AtLeast 版本比较：v >= o（数值化字符串比较，支持任意未来版本号）。
func (v FirmwareVersion) AtLeast(o FirmwareVersion) bool { return CompareVersions(v, o) >= 0 }

// String 版本串原文。
func (v FirmwareVersion) String() string { return string(v) }

// VersionProfile 固件能力/版本探测结果。Version 是已知/占位版本号，
// 后续新固件（1.5.4、1.6.x）自动落入对应能力层，无需改 SDK。
type VersionProfile struct {
	Version            FirmwareVersion
	HasToolIntegrations bool // GET /tool-integrations 可用（≥1.6.0，agent tools 目录）
	HasDrafts          bool // GET /cloth/drafts 可用（1.5.3 时代端点）
	HasUploadedModels  bool // GET /cloth/uploaded-models 可用（≥1.5.0）
	ChunkedProbed      bool // chunk 端点已实测（true 时 Version 结论含分块通道证据）
}

// firmwareState 缓存的探测结果。挂在指针上，Client 值拷贝（Bootstrap/Ping 内部
// 的 c2 := *c）仍共享同一份缓存，且不引入需要锁拷贝检查的 mutex 字段。
type firmwareState struct {
	mu      sync.Mutex
	profile *VersionProfile
}

func (c *Client) fwState() *firmwareState {
	if c.fw == nil { // 仅零值 Client 直构时出现；New() 已初始化
		c.fw = &firmwareState{}
	}
	return c.fw
}

// Firmware 返回固件版本档案（首次调用探测并缓存，之后复用）。
// 探测失败（非 404 的网络/鉴权错误）时返回当时已知的部分档案与错误。
func (c *Client) Firmware() (*VersionProfile, error) {
	st := c.fwState()
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.profile != nil {
		return st.profile, nil
	}
	p := &VersionProfile{}
	var firstErr error
	// tool-integrations 探测：1.6.0 新增端点（agent tools 目录）。
	// 200（目录存在，可能为空数组）= ≥1.6.0；旧固件该路由不存在（404）。
	if _, err := c.GetRaw("/v1/api/data/user/tool-integrations"); err == nil {
		p.HasToolIntegrations = true
	} else if !isNotFound(err) && isAuthOK(err) {
		// 鉴权通过但业务报错（如 403 无权限）也算路由存在
		p.HasToolIntegrations = true
	}
	// drafts 探测：路由不存在（旧固件 404）= 无该能力；路由存在但缺参数（400）
	// 或带参成功 = 有该能力（1.5.3+ 时代新增端点）。带参重试一次以确认。
	if _, err := c.GetRaw("/v1/api/data/user/cloth/drafts"); err == nil {
		p.HasDrafts = true
	} else if !isNotFound(err) {
		if ae, ok := err.(*APIError); ok && ae.StatusCode == http.StatusBadRequest {
			if _, err2 := c.GetRaw("/v1/api/data/user/cloth/drafts?slot_number=1"); err2 == nil || !isNotFound(err2) {
				p.HasDrafts = true
			}
		} else {
			firstErr = err
		}
	}
	if _, err := c.GetRaw("/v1/api/data/user/cloth/uploaded-models"); err == nil {
		p.HasUploadedModels = true
	} else if !isNotFound(err) && firstErr == nil {
		firstErr = err
	}
	switch {
	case p.HasToolIntegrations:
		// tool-integrations 是 1.6.0 新增（1.5.3 无此路由），有即 ≥1.6.0
		p.Version = VerV160
		p.HasDrafts = true // 1.6.0 必然包含 1.5.3 的端点
		p.HasUploadedModels = true
		p.ChunkedProbed = true
	case p.HasDrafts:
		// drafts 端点是 1.5.3 时代新增（1.5.2 构建无此路由），有即 ≥1.5.3
		p.Version = VerV153
		p.ChunkedProbed = true
	case p.HasUploadedModels:
		p.Version = VerV15x
	default:
		p.Version = VerLegacy
	}
	st.profile = p
	return p, firstErr
}

// isAuthOK 对探测而言：鉴权层通过（401/403 之外的错误）说明路由存在。
func isAuthOK(err error) bool {
	ae, ok := err.(*APIError)
	if !ok {
		return false
	}
	return ae.StatusCode != http.StatusUnauthorized && ae.StatusCode != http.StatusForbidden
}

// ForceFirmware 强制指定固件版本（联调/测试用，如 "v1.6.0"），跳过探测且不再被试探改写。
func (c *Client) ForceFirmware(v FirmwareVersion) {
	st := c.fwState()
	st.mu.Lock()
	defer st.mu.Unlock()
	st.profile = &VersionProfile{Version: v, ChunkedProbed: v.AtLeast(VerChunkedUpload)}
}

// fwUpdate 在缓存存在时原地改写（分块试探升格/确认），未探测过则忽略。
func (c *Client) fwUpdate(fn func(*VersionProfile)) {
	st := c.fwState()
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.profile != nil {
		fn(st.profile)
	}
}

// promoteChunked 分块上传整链路成功 → 升格到分块能力层（只升到阈值版本，
// 后续 AtLeast 阈值比较自动覆盖 1.5.4/1.6.x 等新固件）。
func (c *Client) promoteChunked() {
	c.fwUpdate(func(p *VersionProfile) {
		if p.Version.AtLeast(VerChunkedUpload) {
			return
		}
		if p.Version.AtLeast(VerAsyncUpload) { // legacy 判定不被单次试探推翻
			p.Version = VerChunkedUpload
			p.ChunkedProbed = true
		}
	})
}

// confirmAsync chunk 端点不存在（404）→ 确认 async 能力层（无分块通道）。
func (c *Client) confirmAsync() {
	c.fwUpdate(func(p *VersionProfile) {
		if !p.Version.AtLeast(VerChunkedUpload) && p.Version.AtLeast(VerAsyncUpload) {
			p.ChunkedProbed = true
		}
	})
}

// DetectUploadProfile 兼容入口：按新版本模型推导旧的 UploadTier。
//
// Deprecated: 使用 Firmware() + FirmwareVersion 阈值比较；UploadTier 将移除。
func (c *Client) DetectUploadProfile() *UploadProfile {
	p := &UploadProfile{Tier: UploadTierLegacy}
	if fw, _ := c.Firmware(); fw != nil {
		p.HasDrafts = fw.HasDrafts
		p.HasUploadedModels = fw.HasUploadedModels
		switch {
		case fw.Version.AtLeast(VerChunkedUpload):
			p.Tier = UploadTierChunked
		case fw.Version.AtLeast(VerAsyncUpload):
			p.Tier = UploadTierAsync
		default:
			p.Tier = UploadTierLegacy
		}
	}
	return p
}

// isNotFound 判断是否为设备端接口不存在（旧固件 404）。
func isNotFound(err error) bool {
	var apiErr *APIError
	return errAs(err, &apiErr) && apiErr.StatusCode == 404
}

func errAs(err error, target **APIError) bool {
	for err != nil {
		if ae, ok := err.(*APIError); ok {
			*target = ae
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}
