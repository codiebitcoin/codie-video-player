# Codie Video Player

短剧/漫剧桌面播放器，Codie 家族的视频工具。选源、选分类、点开就播——在应用里，或者投到配对的 codie 设备自己的屏幕上。界面有英文、简体中文和日文。

English: [README.md](README.md) · 日本語: [README.ja.md](README.ja.md)

## 源

| 源 | 方式 | 内容 |
|---|---|---|
| 本机 | 内置（`internal/source/local`） | 添加本机文件夹，每个文件夹是一个播放组，里面的视频文件直接播放，不走网络。 |
| YouTube | 插件（`plugins/youtube-music`） | 热门、音乐、游戏、新闻、电影，加上搜索。直播不列出来。 |
| Bilibili | 插件（`plugins/bilibili`） | 热门、排行、最新，登录后还有自己的收藏和合集。 |
| NicoNico | 插件（`plugins/niconico`） | 排行、音乐、动画、游戏，加上搜索。播放走 yt-dlp。需要登录才能看的视频这里不播。 |
| 黄果短剧 | 插件（`plugins/huangguo`） | 短剧：热门、最新、今年和站点自己的分类。随应用打包，默认关闭，在设置里打开。 |

继续观看和追剧按源分开：在一个源里看过的，不会出现在另一个源里。

插件是普通的 JS 目录（`plugin.json` + `index.js`），导出 `videoTabs`、`videoList`、`videoSearch`、`videoItem`、`videoPlay` 即可成为视频源。放进应用的 plugins 目录，或者等首次运行自动装上内置插件。要接新站，写一份插件。

## 投到 codie 设备

1. 点「连接设备」，填设备 IP，输入设备屏幕上的 6 位配对码。
2. 选角色槽位，再播放。剧集会转成设备能收的格式，全屏显示在它的屏幕上。
3. 屏幕已被占用时，播放器会释放占着它的会话，再播一次。
4. 选中 codie 时，音量滑块控制的是设备，不是这台电脑。

## 构建

```bash
go test ./internal/...
go tool mygo build          # 桌面应用，按 mygo.config.ts
make web-run                # 浏览器版，在 127.0.0.1:8080
```

`go tool mygo build -platform darwin/arm64,windows/amd64,linux/amd64` 在一台机器上打出各平台。浏览器版是同一批源的 HTTP 面，在 `cmd/web`。

## 服务器

浏览器版另有一个服务端镜像，和桌面安装包分开。它单独发布为 [ghcr.io/codiebitcoin/codie-video-player-web](https://github.com/users/codiebitcoin/packages/container/package/codie-video-player-web)。桌面发版时一起打：推一个 `v*` 标签会发布这个镜像，不再另开一个 Release。新建的包默认是私有的，要在包的设置里手动改成公开一次（GitHub 没有对应的 API，改成公开后不能再改回）。改公开之前，要用 `codiebitcoin` 先 `docker login ghcr.io` 才能拉取。

```bash
cp deploy/env.example deploy/.env   # 设置 AUTH_USER 和 AUTH_PASS
docker compose -f deploy/docker-compose.yml up -d
```

`AUTH_USER` 和 `AUTH_PASS` 是登录账号。`AUTH_PASS` 留空时，服务自己生成一个密码，在日志里打一次。页面有英文、简体中文和日文，选择跟数据一起保存。本机文件夹是容器能看见的路径，在设置里添加。继续观看和追剧仍按源分开。

## 说明

- 内容均来自第三方站点。本项目不存储、不分发任何音视频，只播放这些站点提供的内容，仅供学习与技术交流。
- HLS 在 macOS 13+ 和 Windows 10+ 上直接播放；Linux 没有 GStreamer 的 HLS 插件时走 ffmpeg 转封装。
