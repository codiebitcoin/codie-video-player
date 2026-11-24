package clt01

import (
	"context"
	"fmt"
	"time"
)

// facade.go — 版本分发的上传/应用流程（SetVrm 的实现内核 + 草稿复用/落盘校验入口）。
// 宿主统一走 atomic.go 的原子语义；本文件的 uploadVRM 是 SetVrm 的执行体。

// VRMOptions 上传选项。
type VRMOptions struct {
	Apply   bool                                 // true（默认）= 上传后立即应用并校验；false = 仅入草稿
	Timeout time.Duration                        // 处理状态轮询上限（0 = 120s）
	OnStep  func(ok bool, detail, errMsg string) // 步骤日志回调（可 nil）
}

func (o *VRMOptions) logf(ok bool, detail, errMsg string) {
	if o != nil && o.OnStep != nil {
		o.OnStep(ok, detail, errMsg)
	}
}

// VRMResult 上传/应用结果。
type VRMResult struct {
	JobID   string
	Version FirmwareVersion // 实际执行层（含首次上传试探后的升格结果）
	Verify  *VerifyResult   // Apply=false 时为 nil
}

// uploadVRM 版本分发的完整上传流程（SetVrm 的执行体）：
//
//	≥1.5.3      : gzip 分块 → 轮询 → 三件套/outfit 应用 → uploaded-models 权威校验
//	1.5.0~1.5.2 : async（404 回退 job/file）→ 轮询 → 三件套 model-apply → custom_cloths 校验
//	<1.5.0      : job + file + complete(忽略404) → 轮询 → outfit 优先回退 PUT（宽松校验）
//
// 1.5.x 基线上首次上传会先试探 ≥1.5.3 分块通道（两者只读接口相同，无法提前区分）：
// 试探成功升格 VerV153 并写回缓存；chunk 端点 404 确认 VerV15x；其余错误取消任务
// 降级 1.5.2 通道，不改缓存。
func (c *Client) uploadVRM(ctx context.Context, cc *CallContext, filename string, data []byte, opt *VRMOptions) (*VRMResult, error) {
	if opt == nil {
		opt = &VRMOptions{Apply: true}
	}
	// 设备（尤其旧固件 Wi-Fi 机型）接收大文件远慢于默认超时，上传链路放宽；
	// 其余短请求不受影响（超时只是上限）。
	c.SetHTTPTimeout(15 * time.Minute)

	fw, _ := c.Firmware()
	version := VerV15x
	if fw != nil {
		version = fw.Version
	}
	opt.logf(true, fmt.Sprintf("%s VRM upload started (%d bytes, firmware %s)", cc.prefix(), len(data), version), "")

	var impl firmwareImpl = implFor(version, c)
	jobID, tier := "", version

	// 1.5.x 基线：先试 ≥1.5.3 分块通道（试探 + 升格逻辑集中在 Facade）
	if !version.AtLeast(VerChunkedUpload) && version.AtLeast(VerAsyncUpload) {
		if id, ok := c.tryChunkedUpload(ctx, cc, filename, data, opt); ok {
			jobID, tier = id, VerChunkedUpload
		}
	}

	if jobID == "" {
		var err error
		jobID, err = impl.UploadVRM(ctx, cc, filename, data)
		if err != nil {
			opt.logf(false, "", err.Error())
			return nil, err
		}
	}
	if tier.AtLeast(VerChunkedUpload) {
		// 分块试探整链路成功 → 缓存升格，后续调用直接走 1.5.3 实现
		c.promoteChunked()
	}

	if err := impl.PollUpload(ctx, jobID, opt.Timeout); err != nil {
		opt.logf(false, "", err.Error())
		return nil, err
	}

	res := &VRMResult{JobID: jobID, Version: tier}
	if !opt.Apply {
		// 仅上传：模型在设备草稿目录，不应用不换屏（apply 由 Status 页/Check&fix 触发）
		opt.logf(true, fmt.Sprintf("%s VRM uploaded (drafts, upload-only, via %s) — apply from Status page", cc.prefix(), tier), "")
		return res, nil
	}

	verify, err := impl.ApplyVRM(ctx, cc, filename)
	res.Verify = verify
	if err != nil {
		opt.logf(false, "", err.Error())
		return res, err
	}
	detail := fmt.Sprintf("%s VRM synced (via %s, evidence: %s)", cc.prefix(), tier, verify.Evidence)
	if verify.Lenient {
		detail = fmt.Sprintf("%s VRM synced (via %s; uploaded-models list empty — known cosmetic issue on this firmware tier, verify model on screen)", cc.prefix(), tier)
	}
	opt.logf(true, detail, "")
	return res, nil
}

// tryChunkedUpload 1.5.x 基线的 ≥1.5.3 通道试探：gzip job 能建 + chunk 全链路成功
// 才算命中；chunk 端点 404 确认 1.5.2；其余失败取消任务走 1.5.2 通道。
// 返回 (jobID, true) 表示已通过分块通道完整上传。
func (c *Client) tryChunkedUpload(ctx context.Context, cc *CallContext, filename string, data []byte, opt *VRMOptions) (string, bool) {
	jobID, err := c.ClothUploadJobCompressedV153(cc.Slot, cc.Cloth, filename, int64(len(data)), UploadTransportGzip)
	if err != nil || jobID == "" {
		return "", false // job 带 gzip transport 建不出来 → 1.5.2 通道
	}
	if err := sendGzipChunks(ctx, c, jobID, data); err != nil {
		if isNotFound(err) {
			fmt.Printf("[clt01] chunk endpoint missing (pre-1.5.3), falling back\n")
			c.confirmAsync()
		} else {
			fmt.Printf("[clt01] chunk upload failed: %v — fallback\n", err)
		}
		_ = c.ClothUploadJobCancel(jobID)
		return "", false
	}
	if err := c.ClothUploadCompleteCompressedV153(jobID, UploadTransportGzip); err != nil {
		fmt.Printf("[clt01] compressed complete failed: %v — fallback\n", err)
		_ = c.ClothUploadJobCancel(jobID)
		return "", false
	}
	return jobID, true
}

// ApplyDraftVRM 设备草稿目录已有该 slot 的同名模型（「仅上传」留下的）时
// 跳过整份重传，直接按本固件层语义应用 + 校验。返回 false 表示没有可复用草稿
// （或固件无 drafts 接口），调用方应走完整上传。
func (c *Client) ApplyDraftVRM(ctx context.Context, cc *CallContext, filename string, opt *VRMOptions) (bool, *VRMResult, error) {
	if opt == nil {
		opt = &VRMOptions{Apply: true}
	}
	c.SetHTTPTimeout(15 * time.Minute)
	drafts, err := c.ClothDrafts(cc.Slot)
	if err != nil {
		return false, nil, nil // 接口不可用 → 按无草稿处理
	}
	base := basename(filename)
	found := false
	for _, d := range drafts {
		if d.ClothID == cc.Cloth && d.Filename == base {
			found = true
			break
		}
	}
	if !found {
		return false, nil, nil
	}
	version := c.detectVersion()
	if !version.AtLeast(VerAsyncUpload) {
		version = VerV15x // 有 drafts 接口说明 ≥1.5.0 能力层，按 async 语义应用
	}
	opt.logf(true, fmt.Sprintf("%s VRM draft already present (%s) — applying without re-upload", cc.prefix(), base), "")
	impl := implFor(version, c)
	verify, err := impl.ApplyVRM(ctx, cc, filename)
	res := &VRMResult{Version: version, Verify: verify}
	if err != nil {
		opt.logf(false, "", err.Error())
		return true, res, err
	}
	opt.logf(true, fmt.Sprintf("%s VRM synced (draft reuse, evidence: %s)", cc.prefix(), verify.Evidence), "")
	return true, res, nil
}

// VerifyVRMPersisted 按固件层校验模型是否真的落盘（防假 SUCCESS）：
//   - ≥1.5.3：uploaded-models 权威，恒空即管线损坏 → false；
//   - 1.5.x / legacy：部分构建列表恒空是已知显示问题 → 宽松返回 true。
func (c *Client) VerifyVRMPersisted(ctx context.Context, cc *CallContext) (bool, error) {
	version := c.detectVersion()
	models, err := c.GetRawCtx(ctx, "/v1/api/data/user/cloth/uploaded-models")
	if err != nil {
		if version.AtLeast(VerChunkedUpload) {
			return false, err // 权威层：列表接口不可用即失败
		}
		return true, nil // 低层固件列表接口不可用 ≠ 未落盘
	}
	if uploadedModelPresentRaw(models, cc.Slot, cc.Cloth) {
		return true, nil
	}
	if version.AtLeast(VerChunkedUpload) {
		return false, nil
	}
	return true, nil // 宽松层：恒空是已知显示问题
}

func basename(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' || p[i] == '\\' {
			return p[i+1:]
		}
	}
	return p
}
