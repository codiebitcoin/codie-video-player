# 播放页改造:沉浸式顶栏 + codie 控制常驻

## 背景

用户反馈播放页两个问题:
1. **丑** — 播放页顶部还是普通页面的顶栏(App 名 + 齿轮 + 连接设备 chip),像网页页头贴在黑视频上;全屏时这排东西也还在。另外 macOS 上播放页没有侧栏,交通灯正好压在返回按钮上(topBar 的 `lead=0` 只对非 darwin 留位,列表页靠侧栏自己避让)。
2. **没有 codie 的控制界面** — codie 已配对但本机播放时(`deviceOutput=false`),播放页上没有任何 codie 控制:`outputToggle`(本机/codie 切换)只在 `a.deviceOutput && a.codie != nil` 时才渲染(ui.go:1335、1415),想投到设备没有入口。而 `codieToggle()`(codieplay.go:357)本身随时可调,会自动重跑 playEpisode 走设备通路。

播放动作(playPause/seekBy/bumpVolume/setRate)已经是双通路,不用动。

## 改动(全在 internal/app/,视图不写测试)

### 1. 沉浸式播放页 — `internal/app/ui.go`

- `view()`: `a.page == pagePlay` 时不再画 `a.topBar(c)`,舞台顶满整窗(交通灯浮在黑场上,标准播放器样式)。
- `playOverlays()` 新增**顶部浮层**,与底部控制条同一套淡入淡出(复用 controlsOverlay 的 `AnimateTo("op")` + `IgnoreInput(!a.controlsOn)` 模式):
  - 左侧:返回箭头(iconBack)+ 剧名 · 集名(白字,SingleLine)。
  - 顶部向下渐变(仿底部 gradient 反向)保证字可读。
  - `DragWindow()`,lead 用 `c.TitleBar().Left`(darwin)/ 72(非 darwin)避窗控。
  - 齿轮、连接设备 chip 不再出现在播放页(设置与配对仍在列表页顶栏;codie 状态由控制条里的切换 pill 呈现)。
- Esc 返回、单击唤醒控制等现有交互不变。

### 2. codie 控制常驻 — `ui.go` controlsBody / outputToggle

- `a.codie != nil` 就渲染 `outputToggle`(本机 | codie 两段 pill),本机模式也显示——点「codie」即 `a.codieToggle(true)` 投到设备,设备模式点「本机」拉回来。
- 设备模式已有的 codieScrubber / codiePlayPause / codieVolumeControl 保持不变。

### 3. 控制条细节 — `ui.go`

- 进度行改成 `已播 | scrubber | 时长`(本地取 player、设备取 codieTimes,复用 `clock()`);去掉右下角按钮排里的 `1:23 / 12:00` 文本,那格留给音量。
- 其余(选集抽屉、倍速、小窗、全屏)不动。

### 4. 走查补一张图 — `internal/app/debug_shots.go`

- 在 4d 后加一步:`deviceOutput=false` + 假 codie(复用现成 codieout.New 假件)+ controlsOn=true,存 `4e-local-paired`,验证本机模式能看到 codie 切换。

## 验证

1. `go build ./...`、`go test ./internal/...` 全绿(现有测试不联网)。
2. `MM_SHOTS=/tmp/codie-shots go run .` 跑一遍走查(数据隔离到临时目录,约 70 秒),亲自看 4-playing / 4b / 4c / 4d / 4e 几张实拍,不满意就地微调再拍。
3. i18n:全部复用现有 key("本机"、"codie" 等已在 zh_cn.go),如冒出新字符串按约定补进 `internal/i18n/zh_cn.go`。