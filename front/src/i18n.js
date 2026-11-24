// i18n 现在只有中文；结构照 life-connect 的字典，留了以后加语言的位置。
const zh = {
  appName: 'Codie Video',
  login: '登录',
  logout: '退出',
  username: '用户名',
  password: '密码',
  signInError: '登录',
  searchPlaceholder: '搜短剧、漫剧、视频…',
  search: '搜索',
  continueWatching: '继续观看',
  following: '追剧',
  follow: '追剧',
  followingOn: '已追',
  lists: '列表',
  history: '观看历史',
  searchList: '搜索列表',
  noHistory: '还没有观看记录',
  noFollows: '还没有追剧',
  noMatch: '没有匹配「%s」的记录',
  ep: '第%d集',
  epOf: '第%d集，共%d集',
  loading: '加载中…',
  loadMore: '加载更多',
  empty: '这里还没有内容',
  noResults: '没找到',
  play: '播放',
  resume: '从 %.0f 秒继续',
  next: '下一集',
  prev: '上一集',
  upNext: '%d 秒后自动连播',
  playNow: '立即播放',
  cancel: '取消',
  speed: '倍速',
  error: '出错了',
  retry: '重试',
  starting: '正在取流…',
  transcoding: '转码中，画面马上就好…',
  back: '返回',
}

export function t(key, ...args) {
  let s = zh[key] ?? key
  let i = 0
  s = s.replace(/%(\.0)?[dfs]/g, (m) => {
    const v = args[i++]
    if (m === '%.0f') return String(Math.round(v))
    return v == null ? m : String(v)
  })
  return s
}
