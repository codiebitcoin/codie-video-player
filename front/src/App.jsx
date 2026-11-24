import { useEffect, useState } from 'react'
import { get, Unauthorized } from './api.js'
import { t } from './i18n.js'
import LoginPage from './pages/LoginPage.jsx'
import HomePage from './pages/HomePage.jsx'
import ListPage from './pages/ListPage.jsx'
import DetailPage from './pages/DetailPage.jsx'
import PlayerPage from './pages/PlayerPage.jsx'

// 路由是手写的（家族惯例）：路径就是状态，navigate 用 History API。
export function navigate(path) {
  window.history.pushState({}, '', path)
  window.dispatchEvent(new PopStateEvent('popstate'))
}

export default function App() {
  const [route, setRoute] = useState(parsePath())
  const [authed, setAuthed] = useState(null) // null = 还不知道

  useEffect(() => {
    const onPop = () => setRoute(parsePath())
    window.addEventListener('popstate', onPop)
    return () => window.removeEventListener('popstate', onPop)
  }, [])

  useEffect(() => {
    // 用一个便宜的 API 问一声有没有登录。
    get('/api/health')
      .then(() => {})
      .catch(() => {})
    fetch('/api/sources')
      .then((res) => setAuthed(res.status !== 401))
      .catch(() => setAuthed(false))
  }, [])

  if (authed === null) {
    return <div className="page-center">{t('loading')}</div>
  }
  if (!authed) {
    return (
      <LoginPage
        onLogin={() => {
          setAuthed(true)
          setRoute(parsePath())
        }}
      />
    )
  }

  if (route === '/list') {
    return <ListPage />
  }
  const m = route.match(/^\/detail\/([^/]+)\/(.+)$/)
  if (m) {
    return <DetailPage src={m[1]} id={decodeURIComponent(m[2])} />
  }
  const w = route.match(/^\/watch\/([^/]+)\/([^/]+)\/([^/]+)$/)
  if (w) {
    return (
      <PlayerPage
        src={w[1]}
        id={decodeURIComponent(w[2])}
        ep={decodeURIComponent(w[3])}
      />
    )
  }
  return <HomePage route={route} />
}

function parsePath() {
  return window.location.pathname
}
