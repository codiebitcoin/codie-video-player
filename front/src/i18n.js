// The web face speaks the same three languages as the desktop: English is the
// key, and the default. A missing line falls back to English.

const zh = {
  'Codie Video': 'Codie 视频',
  Username: '用户名',
  Password: '密码',
  'Sign in': '登录',
  'Sign out': '退出',
  'Wrong username or password': '用户名或密码不对',
  'Search dramas, videos…': '搜短剧、视频…',
  Search: '搜索',
  'Continue Watching': '继续观看',
  Following: '追剧',
  Follow: '追剧',
  'My Favorites': '我的收藏',
  History: '观看历史',
  'Nothing watched yet': '还没有观看记录',
  'Nothing followed yet': '还没有追剧',
  'Nothing found for “%s”': '没有找到「%s」',
  'No match for “%s”': '没有匹配「%s」的记录',
  'Ep %d': '第%d集',
  'Ep %d of %d': '第%d集，共%d集',
  '%d episodes': '%d 集',
  '%d videos': '%d 个视频',
  Loading: '加载中…',
  'Load More': '加载更多',
  'Nothing here yet': '这里还没有内容',
  Play: '播放',
  'Play Episode 1': '播放第 1 集',
  'Continue · Ep %d': '继续播放 · 第%d集',
  Next: '下一集',
  Previous: '上一集',
  'Up next in %d s': '%d 秒后自动连播',
  'Play now': '立即播放',
  Cancel: '取消',
  Speed: '倍速',
  Retry: '重试',
  'Getting the stream…': '正在取流…',
  'Transcoding — the picture is on its way…': '转码中，画面马上就好…',
  Back: '返回',
  All: '全部',
  Playlist: '播放列表',
  Playlists: '播放列表',
  Shuffle: '随机播放',
  'Add Folder': '添加文件夹',
  'Folder path on the server': '服务器上的文件夹路径',
  Add: '添加',
  Remove: '移除',
  'Add a folder of videos to play it here': '添加一个视频文件夹，在这里播放',
  'Search in playlist…': '在播放列表里搜索…',
  Completed: '已完结',
  Ongoing: '连载中',
  'Score %s': '评分 %s',
  Finished: '已看完',
  Episodes: '选集',
  'No playable episodes for this drama': '这部剧没有可以播的集',
  Settings: '设置',
  Language: '语言',
  Sources: '源',
  Plugins: '插件',
  Close: '关闭',
  Local: '本机',
  'This browser can’t play this stream': '这个浏览器播不了这个流',
  'Playback failed. Try again.': '播放出错，稍后重试',
  'Recent Searches': '最近搜索',
}

const ja = {
  'Codie Video': 'Codie ビデオ',
  Username: 'ユーザー名',
  Password: 'パスワード',
  'Sign in': 'ログイン',
  'Sign out': 'ログアウト',
  'Wrong username or password': 'ユーザー名かパスワードが違います',
  'Search dramas, videos…': 'ドラマ、動画を検索…',
  Search: '検索',
  'Continue Watching': '続きを見る',
  Following: 'フォロー中',
  Follow: 'フォロー',
  'My Favorites': 'マイお気に入り',
  History: '視聴履歴',
  'Nothing watched yet': 'まだ視聴履歴がありません',
  'Nothing followed yet': 'まだフォローがありません',
  'Nothing found for “%s”': '「%s」は見つかりませんでした',
  'No match for “%s”': '「%s」に一致する記録がありません',
  'Ep %d': '第%d話',
  'Ep %d of %d': '第%d話 / 全%d話',
  '%d episodes': '%d 話',
  '%d videos': '%d 本',
  Loading: '読み込み中…',
  'Load More': 'もっと見る',
  'Nothing here yet': 'まだ何もありません',
  Play: '再生',
  'Play Episode 1': '第1話を再生',
  'Continue · Ep %d': '続きから · 第%d話',
  Next: '次の話',
  Previous: '前の話',
  'Up next in %d s': '%d 秒後に次を再生',
  'Play now': '今すぐ再生',
  Cancel: 'キャンセル',
  Speed: '速度',
  Retry: '再試行',
  'Getting the stream…': 'ストリームを取得しています…',
  'Transcoding — the picture is on its way…': '変換中です。まもなく再生できます…',
  Back: '戻る',
  All: 'すべて',
  Playlist: 'プレイリスト',
  Playlists: 'プレイリスト',
  Shuffle: 'シャッフル',
  'Add Folder': 'フォルダを追加',
  'Folder path on the server': 'サーバー上のフォルダのパス',
  Add: '追加',
  Remove: '削除',
  'Add a folder of videos to play it here': '動画のフォルダを追加すると、ここで再生できます',
  'Search in playlist…': 'プレイリスト内を検索…',
  Completed: '完結',
  Ongoing: '連載中',
  'Score %s': '評価 %s',
  Finished: '見終わり',
  Episodes: '話一覧',
  'No playable episodes for this drama': '再生できる話がありません',
  Settings: '設定',
  Language: '言語',
  Sources: 'ソース',
  Plugins: 'プラグイン',
  Close: '閉じる',
  Local: 'このサーバー',
  'This browser can’t play this stream': 'このブラウザでは再生できません',
  'Playback failed. Try again.': '再生に失敗しました。もう一度試してください',
  'Recent Searches': '最近の検索',
}

const catalogs = { 'zh-CN': zh, ja }

let language = localStorage.getItem('cvp-lang') || 'en'
const listeners = new Set()

export function getLanguage() {
  return language
}

export function setLanguage(code) {
  language = catalogs[code] ? code : 'en'
  localStorage.setItem('cvp-lang', language)
  document.documentElement.lang = language
  listeners.forEach((fn) => fn(language))
}

export function onLanguage(fn) {
  listeners.add(fn)
  return () => listeners.delete(fn)
}

export function t(key, ...args) {
  const table = catalogs[language]
  let s = (table && table[key]) || key
  let i = 0
  s = s.replace(/%(\d+\$)?[dfs]/g, (m) => {
    const numbered = /^%(\d+)\$/.exec(m)
    const v = numbered ? args[Number(numbered[1]) - 1] : args[i++]
    return v == null ? m : String(v)
  })
  return s
}

document.documentElement.lang = language
