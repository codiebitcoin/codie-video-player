package clt01

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

// api_v152.go — 固件 1.5.0 ~ 1.5.2 实现（codie-black 实测验证，见 PLAN.md §2.3 / §4）：
//   - 上传：POST /cloth/upload/async 单请求 multipart（该层无 8MiB 限制，整包可发）；
//     接口不存在（404）回退 job + job/:id/file。
//   - 应用：PUT /character 三件套 model-apply（selected_cloth + available_cloths +
//     link_other_original_cloth 全部显式携带，空串也要带）触发固件
//     commitDraftClothCache 把草稿转正式；outfit/apply 在该层可能 200 但不提交
//     草稿，不可作为应用依据。
//   - 落盘判据：PUT 响应/回读的 custom_cloths（scanAppliedClothCache 回填，含刚
//     提交的文件名）；uploaded-models 部分构建扫硬编码根路径恒空，不可信。

type v152Impl struct{ c *Client }

func (v *v152Impl) Version() FirmwareVersion { return VerV15x }

// UploadVRM async 单请求优先，404 回退 job/file。
func (v *v152Impl) UploadVRM(ctx context.Context, cc *CallContext, filename string, data []byte) (string, error) {
	jobID, err := v.c.ClothUploadAsync(cc.Slot, cc.Cloth, filename, data)
	if err == nil && jobID != "" {
		return jobID, nil
	}
	if err != nil && !isNotFound(err) {
		// async 接口存在但报错：仍尝试 job/file 一次（与实测通过的兜底一致）
		fmt.Printf("[clt01] async upload failed (%v) — trying job/file\n", err)
	}
	id, lerr := v.c.legacyUploadJobFile(cc.Slot, cc.Cloth, filename, data)
	if lerr != nil {
		return "", lerr
	}
	return id, nil
}

func (v *v152Impl) PollUpload(ctx context.Context, jobID string, timeout time.Duration) error {
	return pollUploadJob(ctx, v.c, jobID, timeout)
}

func (v *v152Impl) SelectCloth(ctx context.Context, slot, cloth int) error {
	_, err := v.c.SelectClothV15x(slot, cloth)
	return err
}

// ApplyVRM 三件套 model-apply + custom_cloths 落盘证据校验。
func (v *v152Impl) ApplyVRM(ctx context.Context, cc *CallContext, filename string) (*VerifyResult, error) {
	customCloths, err := v.c.SelectClothV15x(cc.Slot, cc.Cloth)
	if err != nil {
		return &VerifyResult{Persisted: false}, fmt.Errorf("clt01: model-apply: %w", err)
	}
	base := filepath.Base(filename)
	if !strings.Contains(customCloths, base) {
		if ch, gerr := v.c.GetCharacterCtx(ctx, cc.Slot); gerr == nil {
			customCloths = ch.CustomCloths
		}
	}
	if !strings.Contains(customCloths, base) {
		return &VerifyResult{Persisted: false, Evidence: customCloths},
			fmt.Errorf("%s clt01: VRM not committed by device (custom_cloths=%q lacks %q) — draft commit failed on firmware 1.5.x", cc.prefix(), customCloths, base)
	}
	return &VerifyResult{Persisted: true, Evidence: "custom_cloths=" + customCloths}, nil
}

func (v *v152Impl) CancelUpload(jobID string) error { return v.c.ClothUploadJobCancel(jobID) }
