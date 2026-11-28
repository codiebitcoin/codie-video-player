// Codie Video Player: 短剧/漫剧桌面播放器,Codie 家族的视频工具。
// 分类(短剧/漫剧/AI魔改/AI换脸)+ 年份 + 排行 + 搜索,不涉及任何音乐概念。
export default ({ command }: { command: string }) => ({
  name: "Codie Video Player",
  identifier: "com.codie.videoplayer",
  slug: "codie-video-player",
  version: "0.1.43",
  out: "build",
  urlSchemes: [command === "dev" ? "codie-video-player-dev" : "codie-video-player"],
  macos: {
    icon: "icon/AppIcon.icon",
    minimumSystemVersion: "13.0",
  },
  linux: {
    comment: "A video player for short dramas",
    categories: ["AudioVideo", "Video", "Player"],
    webview: false,
    // 只出 deb:发行版装它一个就够,别的不再传。
    formats: ["deb"],
  },
});
