# Codie Video Player

短編ドラマと動画のためのデスクトッププレーヤーで、CODE27 家族のビデオツールです。ソースを選び、分類を選んで再生します。アプリの中でも、ペアリングした codie デバイスの画面でも再生できます。表示言語は英語、简体中国語、日本語です。

English: [README.md](README.md) · 简体中文: [README.zh-CN.md](README.zh-CN.md)

## ソース

| ソース | 方式 | 内容 |
|---|---|---|
| この Mac | 組み込み（`internal/source/local`） | このコンピュータのフォルダを追加します。フォルダごとに一つのグループになり、中の動画ファイルをネットワークを使わずに再生します。 |
| YouTube | プラグイン（`plugins/youtube-music`） | 人気、音楽、ゲーム、ニュース、映画、そして検索。ライブ配信は表示しません。 |
| Bilibili | プラグイン（`plugins/bilibili`） | 人気、ランキング、最新。ログインするとお気に入りとコレクションも使えます。 |
| NicoNico | プラグイン（`plugins/niconico`） | ランキング、音楽、アニメ、ゲーム、そして検索。再生は yt-dlp を使います。ログインが必要な動画は再生しません。 |
| 黄果ドラマ | プラグイン（`plugins/huangguo`） | 短編ドラマ。人気、最新、今年、サイト自身の分類。アプリに同梱され、設定でオンにするまでは無効です。 |

「続きを見る」と「フォロー中」はソースごとに保持されます。あるソースで見た作品は、別のソースには表れません。

プラグインは普通の JavaScript ディレクトリ（`plugin.json` + `index.js`）で、`videoTabs`、`videoList`、`videoSearch`、`videoItem`、`videoPlay` を書き出すとソースになります。アプリの plugins フォルダに置くか、初回起動時に同梱のプラグインが入ります。新しいサイトはプラグインを書けば足ります。

## codie デバイスで再生

1. デバイスを接続し、デバイスの IP と画面に出る 6 桁のコードを入力します。
2. キャラクタースロットを選んで再生します。話はデバイスが受け取れる形式に変換され、画面一杯に表示されます。
3. 画面が使用中のときは、占有しているセッションを解放してからもう一度再生します。
4. codie を選んでいる間、音量スライダーはこのコンピュータではなくデバイスを操作します。

## ビルド

```bash
go test ./internal/...
go tool mygo build          # デスクトップアプリ。mygo.config.ts に従う
make web-run                # ブラウザ版。127.0.0.1:8080
```

`go tool mygo build -platform darwin/arm64,windows/amd64,linux/amd64` で、一台のマシンから各プラットフォームをビルドできます。ブラウザ版は同じソースの HTTP 版で、`cmd/web` にあります。

## サーバー

ブラウザ版はデスクトップのインストーラーとは別のサーバイメージです。パッケージは [ghcr.io/codiebitcoin/codie-video-player-web](https://github.com/users/codiebitcoin/packages/container/package/codie-video-player-web) で、デスクトップのリリースと一緒に作られます。`v*` タグを push するとイメージが公開され、別の Release は作られません。新しいパッケージは非公開で始まります。パッケージ設定で一度だけ公開にしてください（GitHub にこの操作の API はなく、公開にした後は戻せません）。公開にするまでは、`codiebitcoin` で `docker login ghcr.io` してから pull します。

```bash
cp deploy/env.example deploy/.env   # AUTH_USER と AUTH_PASS を設定
docker compose -f deploy/docker-compose.yml up -d
```

`AUTH_USER` と `AUTH_PASS` がログインです。`AUTH_PASS` を空にすると、サーバーがパスワードを作り、ログに一度だけ出します。表示言語は英語、简体中国語、日本語で、選んだ言語はデータと一緒に保存されます。ローカルのフォルダはコンテナから見えるパスで、設定から追加します。「続きを見る」と「フォロー中」はソースごとに分かれます。

## 注意

- 内容はすべて第三者のサイトから来ます。このプロジェクトは音声や動画を保存も配布もせず、それらのサイトが提供するものを再生するだけです。学習と技術交流のためのものです。
- HLS は macOS 13+ と Windows 10+ でそのまま再生します。Linux で GStreamer に HLS プラグインがない場合は ffmpeg を使います。
