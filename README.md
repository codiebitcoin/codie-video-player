# Codie Video Player

A desktop player for short dramas and videos, made for the CODE27 family. Pick a source, pick a category, and play — in the app, or on a paired codie device's own screen. It speaks English, 简体中文 and 日本語.

简体中文: [README.zh-CN.md](README.zh-CN.md) · 日本語: [README.ja.md](README.ja.md)

## Sources

| Source | How | What it plays |
|---|---|---|
| Local | Built in (`internal/source/local`) | Folders you add on this computer. Each folder is a group; every video file in it plays directly, with no network. |
| YouTube | Plugin (`plugins/youtube-music`) | Popular, Music, Gaming, News and Movies, plus search. Live streams are left out. |
| Bilibili | Plugin (`plugins/bilibili`) | Popular, ranking and latest, plus your favorites and collections once you sign in. |
| NicoNico | Plugin (`plugins/niconico`) | Ranking, Music, Anime and Games, plus search. A play goes through yt-dlp. Videos that need a NicoNico sign-in aren't played. |
| 黄果短剧 | Plugin (`plugins/huangguo`) | Short dramas: popular, latest, this year and the site's own categories. Shipped with the app and off until you turn it on in Settings. |

Continue Watching and Following are kept per source, so what you watched on one never shows on another.

A plugin is an ordinary directory of JavaScript (`plugin.json` + `index.js`) that exports `videoTabs`, `videoList`, `videoSearch`, `videoItem` and `videoPlay`. Drop it in the app's plugins folder, or let the first run install the ones the app ships. To add a site, write a plugin.

## Play on a codie device

1. Connect Device, fill in the device's IP, and enter the 6-digit code its screen shows.
2. Pick a character slot, then play. The episode is transcoded to what the device accepts and shown fullscreen on its screen.
3. If the screen is already taken, the player releases the session holding it and plays again.
4. While codie is selected, the volume slider controls the device, not this computer.

## Build

```bash
go test ./internal/...
go tool mygo build          # the desktop app, from mygo.config.ts
make web-run                # the browser face, on 127.0.0.1:8080
```

`go tool mygo build -platform darwin/arm64,windows/amd64,linux/amd64` builds every platform from one machine. The web face is the same sources over HTTP, in `cmd/web`.

## Server

The browser face is also a server image, separate from the desktop installers. It is published as its own package, [ghcr.io/codiebitcoin/codie-video-player-web](https://github.com/users/codiebitcoin/packages/container/package/codie-video-player-web), and a desktop release builds it too — pushing a `v*` tag publishes the image, without opening another Release. A new package starts private; set it to public once in the package settings (GitHub has no API for that, and it cannot be undone). Until then, `docker login ghcr.io` as `codiebitcoin` is required to pull it.

```bash
cp deploy/env.example deploy/.env   # set AUTH_USER and AUTH_PASS
docker compose -f deploy/docker-compose.yml up -d
```

`AUTH_USER` and `AUTH_PASS` are the login. Leave `AUTH_PASS` empty and the server makes a password and prints it once in the log. The page speaks English, 简体中文 and 日本語; the choice is kept with the data. Local folders are paths on the machine the container sees, added in Settings, and Continue Watching and Following stay with the source they came from.

## Notes

- Content comes from third-party sites. This project stores none of it and redistributes none of it; it only plays what those sites serve, for learning and technical exchange.
- HLS plays on macOS 13+ and Windows 10+. On Linux it goes through ffmpeg when GStreamer has no HLS plugins.
