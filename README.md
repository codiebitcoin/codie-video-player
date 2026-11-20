# Codie Video Player

短剧/漫剧桌面播放器 —— Codie 家族的视频工具，按视频工具的思路设计：分类（短剧/漫剧/AI魔改/AI换脸）、今年、排行、搜索，网格直达详情和播放，没有音乐、文件夹、歌曲、专辑这些概念。

## 源

| 源 | 方式 | 内容 |
|---|---|---|
| 黄果短剧 | 内置 Go 客户端（`internal/source/huangguo`） | 热门/最新/今年 + 站点分类，HLS 直播，封面 AES 解密 |
| Bilibili | 插件（`plugins/bilibili`） | 热门/最新 + 搜索，DASH 视音分离经 ffmpeg 合流 |
| YouTube Music | 插件（`plugins/youtube-music`） | 搜索，yt-dlp 下载合并 mp4 |

插件是普通 JS 目录（`plugin.json` + `index.js`），实现 `videoTabs / videoList / videoSearch / videoItem / videoPlay` 这些导出即可成为视频源；放进应用的 plugins 目录（或首次运行自动装内置插件）即可被发现。要接新站，改一份插件就好。

## 用

1. 顶栏「连接设备」填 codie 设备 IP，输入设备屏幕上的 6 位配对码
2. 配对后自动探测设备能力（`media/capabilities` 会自己报它能放什么）
3. 选源 → 选分类 → 点剧 → 点集，视频在应用里播

## 构建

```bash
go build ./...
go run .        # 本地起一个
```

Windows / Linux 打包与 mygo 的一致（`mygo.config.ts`）。

## 说明

- 内容均来自第三方站点，本项目不存储、不分发任何音视频，仅供学习与技术交流
- 播放 HLS 需要 macOS 13+ / Windows 10+；Linux 需要 GStreamer（缺省走 ffmpeg 转封装）
