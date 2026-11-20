# AGENTS.md

## Tests

只为真正会坏的行为写测试。解析（目录/搜索/详情/播放地址的 JSON 形状）、封面 AES 解密、参数拼装这类写单测（httptest 假站点）；视图本身不测——按键点到什么状态那类例外，以后需要时用 ui.NewTester。

**怎么跑：**

- `go test ./internal/...`
- 提交前全绿；`internal/source/huangguo` 的测试不联网（httptest）

## 约定

- UI 字符串直接写中文（面向中文短剧场景），不设 i18n catalog；插件自己的翻译走它们的 locales/
- 状态只在主线程读写；加载一律 goroutine + post
- 插件协议见 `internal/plugin/plugin.go` 包注释与 `internal/video/video.go`
