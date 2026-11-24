package clt01

import (
	"context"
	"fmt"
	"time"
)

// api_legacy.go — 固件 <1.5.0 实现（见 PLAN.md §2.3 / §4）：
//   - 上传：job + job/:id/file；complete 可能不存在（404 忽略）。
//   - 应用：outfit/apply 优先，404/405 回退 PUT 三件套（复用 SelectCloth 历史逻辑）。
//   - 落盘判据：宽松 —— uploaded-models 扫硬编码根路径恒空不能证明失败，
//     以 SUCCESS + SelectCloth 回读为准。

type legacyImpl struct{ c *Client }

func (v *legacyImpl) Version() FirmwareVersion { return VerLegacy }

func (v *legacyImpl) UploadVRM(ctx context.Context, cc *CallContext, filename string, data []byte) (string, error) {
	id, err := v.c.legacyUploadJobFile(cc.Slot, cc.Cloth, filename, data)
	if err != nil {
		return "", err
	}
	return id, nil
}

func (v *legacyImpl) PollUpload(ctx context.Context, jobID string, timeout time.Duration) error {
	return pollUploadJob(ctx, v.c, jobID, timeout)
}

func (v *legacyImpl) SelectCloth(ctx context.Context, slot, cloth int) error {
	// 设备端 <1.5.0 无实测样本：保留 outfit/apply 优先 + 404 回退三件套的旧兼容逻辑
	return cSelectClothLegacy(v.c, slot, cloth)
}

// ApplyVRM 宽松校验：列表恒空是已知显示问题，不判失败。
func (v *legacyImpl) ApplyVRM(ctx context.Context, cc *CallContext, filename string) (*VerifyResult, error) {
	if err := v.SelectCloth(ctx, cc.Slot, cc.Cloth); err != nil {
		return &VerifyResult{Persisted: false}, fmt.Errorf("clt01: select cloth: %w", err)
	}
	res := &VerifyResult{Lenient: true}
	if models, merr := v.c.GetRawCtx(ctx, "/v1/api/data/user/cloth/uploaded-models"); merr == nil && uploadedModelPresentRaw(models, cc.Slot, cc.Cloth) {
		res.Persisted = true
		res.Evidence = "uploaded-models"
		return res, nil
	}
	res.Persisted = true // 宽松层：未证明也未证伪，屏幕实况为准
	return res, nil
}

func (v *legacyImpl) CancelUpload(jobID string) error { return v.c.ClothUploadJobCancel(jobID) }
