import { useEffect, useState } from 'react'
import { get, post, Unauthorized } from './api.js'
import { getLanguage, onLanguage, setLanguage, t } from './i18n.js'
import LoginPage from './pages/LoginPage.jsx'
import BrowsePage from './pages/BrowsePage.jsx'
import DetailPage from './pages/DetailPage.jsx'
import PlayerPage from './pages/PlayerPage.jsx'
import SettingsDialog from './pages/SettingsDialog.jsx'

export function navigate(path) {
  window.history.pushState({}, '', path)
  window.dispatchEvent(new PopStateEvent('popstate'))
}

export default function App() {
  const [lang, setLang] = useState(getLanguage())
  const [route, setRoute] = useState(window.location.pathname + window.location.search)
  const [authed, setAuthed] = useState(null)

  useEffect(() => onLanguage(setLang), [])
  useEffect(() => {
    const onPop = () => setRoute(window.location.pathname + window.location.search)
    window.addEventListener('popstate', onPop)
    return () => window.removeEventListener('popstate', onPop)
  }, [])

  useEffect(() => {
    fetch('/api/sources', { credentials: 'same-origin' })
      .then((res) => setAuthed(res.status !== 401))
      .catch(() => setAuthed(false))
  }, [])

  if (authed === null) {
    return <div className="page-center hint">{t('Loading')}</div>
  }
  if (!authed) {
    return <LoginPage onLogin={() => setAuthed(true)} />
  }

  const path = route.split('?')[0]
  const watch = path.match(/^\/watch\/([^/]+)\/([^/]+)\/([^/]+)$/)
  if (watch) {
    return (
      <PlayerPage
        key={lang + path}
        src={decodeURIComponent(watch[1])}
        id={decodeURIComponent(watch[2])}
        ep={decodeURIComponent(watch[3])}
      />
    )
  }

  return <Shell lang={lang} route={route} onSignedOut={() => setAuthed(false)} />
}

function Shell({ lang, route, onSignedOut }) {
  const path = route.split('?')[0]
  const params = new URLSearchParams(route.split('?')[1] || '')
  const query = params.get('q') || ''
  const detail = path.match(/^\/detail\/([^/]+)\/(.+)$/)

  const [sources, setSources] = useState([])
  // The source you were on survives a remount: the watch page takes the whole
  // window and the shell with it, so coming back home starts fresh here.
  const [src, setSrc] = useState(() => localStorage.getItem('cvp-src') || '')
  const [tabs, setTabs] = useState([])
  const [tab, setTab] = useState('')
  const [tabsErr, setTabsErr] = useState('')
  const [q, setQ] = useState(query)
  const [favOpen, setFavOpen] = useState(true)
  const [settingsOpen, setSettingsOpen] = useState(false)
  const [recent, setRecent] = useState(() => readRecent())
  const [searchFocus, setSearchFocus] = useState(false)

  useEffect(() => setQ(query), [query])

  // pickSource is where the sidebar stands: kept for the next homecoming, and
  // told to the server so its fallback agrees.
  const pickSource = (id, tellServer) => {
    setSrc(id)
    localStorage.setItem('cvp-src', id)
    if (tellServer) post('/api/settings', { source: id }).catch(() => {})
  }

  const reloadSources = () =>
    get('/api/sources').then((d) => {
      const list = d.sources || []
      setSources(list)
      setSrc((cur) => {
        // What page is actually on decides first: a detail of another source
        // means that source is the one in use, not the one the sidebar had.
        const on = window.location.pathname.match(/^\/detail\/([^/]+)\//)
        const here = on ? decodeURIComponent(on[1]) : ''
        for (const id of [here, cur, localStorage.getItem('cvp-src') || '', d.current || '']) {
          if (id && list.some((s) => s.id === id)) return id
        }
        return list[0]?.id || ''
      })
    })

  useEffect(() => {
    reloadSources().catch((e) => {
      if (e instanceof Unauthorized) onSignedOut()
    })
    get('/api/settings')
      .then((d) => {
        if (d.language && d.language !== getLanguage()) setLanguage(d.language)
      })
      .catch(() => {})
  }, [])

  // A drama opened from search or a shelf can be of another source; the
  // sidebar follows it, as the desktop's detail page does, so going back home
  // lands where the drama came from instead of the old source.
  useEffect(() => {
    if (!detailSrc || detailSrc === src) return
    if (sources.length && !sources.some((s) => s.id === detailSrc)) return
    pickSource(detailSrc, false)
  }, [detailSrc, sources])

  useEffect(() => {
    if (!src) return
    setTabs([])
    setTabsErr('')
    get(`/api/source/${encodeURIComponent(src)}/tabs`)
      .then((d) => {
        const list = d.tabs || []
        setTabs(list)
        setTabsErr(d.error || '')
        setTab((cur) => (list.some((tb) => tb.id === cur) ? cur : list[0]?.id || ''))
      })
      .catch((e) => {
        if (e instanceof Unauthorized) onSignedOut()
        else setTabsErr(e.message)
      })
  }, [src, lang])

  const chooseSource = (id) => {
    pickSource(id, true)
    setTab('')
    if (path !== '/') navigate('/')
  }

  const submitSearch = (e) => {
    e?.preventDefault()
    const text = q.trim()
    if (!text) {
      navigate('/')
      return
    }
    const next = [text, ...recent.filter((s) => s !== text)].slice(0, 8)
    setRecent(next)
    localStorage.setItem('cvp-recent', JSON.stringify(next))
    navigate(`/?q=${encodeURIComponent(text)}`)
  }

  const signOut = async () => {
    try {
      await post('/api/logout')
    } catch {}
    onSignedOut()
  }

  const favs = tabs.filter((tb) => tb.id.startsWith('fav:') || tb.id.startsWith('season:'))
  const mainTabs = tabs.filter((tb) => !tb.id.startsWith('fav:') && !tb.id.startsWith('season:'))
  const searching = !!query && !detail
  const tabLabel = tabs.find((tb) => tb.id === tab)?.label || ''

  return (
    <div className="app" data-lang={lang}>
      <aside className="sidebar">
        <label className="source-select">
          <span className="sr">{t('Sources')}</span>
          <select value={src} onChange={(e) => chooseSource(e.target.value)}>
            {sources.map((s) => (
              <option key={s.id} value={s.id}>
                {s.name}
              </option>
            ))}
          </select>
        </label>

        <form className="search" onSubmit={submitSearch}>
          <SearchIcon />
          <input
            value={q}
            placeholder={t('Search dramas, videos…')}
            aria-label={t('Search')}
            onChange={(e) => setQ(e.target.value)}
            onFocus={() => setSearchFocus(true)}
            onBlur={() => setTimeout(() => setSearchFocus(false), 150)}
          />
          {q && (
            <button
              type="button"
              className="icon-btn"
              aria-label={t('Cancel')}
              onClick={() => {
                setQ('')
                if (searching) navigate('/')
              }}
            >
              ×
            </button>
          )}
          {searchFocus && !q && recent.length > 0 && (
            <div className="recent" role="listbox" aria-label={t('Recent Searches')}>
              {recent.map((s) => (
                <button
                  type="button"
                  key={s}
                  onMouseDown={(e) => {
                    e.preventDefault()
                    setQ(s)
                    navigate(`/?q=${encodeURIComponent(s)}`)
                  }}
                >
                  {s}
                </button>
              ))}
            </div>
          )}
        </form>

        <nav className="nav">
          {mainTabs.map((tb) => (
            <button
              key={tb.id}
              className={'nav-item' + (!searching && !detail && tab === tb.id ? ' on' : '')}
              onClick={() => {
                setTab(tb.id)
                if (path !== '/' || query) navigate('/')
              }}
            >
              <NavGlyph id={tb.id} />
              <span>{tb.label}</span>
            </button>
          ))}
          {favs.length > 0 && (
            <>
              <button className="nav-group" onClick={() => setFavOpen((v) => !v)}>
                <span>{t('My Favorites')}</span>
                <span className="chev">{favOpen ? '▾' : '▸'}</span>
              </button>
              {favOpen &&
                favs.map((tb) => (
                  <button
                    key={tb.id}
                    className={'nav-item sub' + (!searching && !detail && tab === tb.id ? ' on' : '')}
                    onClick={() => {
                      setTab(tb.id)
                      if (path !== '/' || query) navigate('/')
                    }}
                  >
                    <NavGlyph id={tb.id} />
                    <span>{tb.label}</span>
                  </button>
                ))}
            </>
          )}
        </nav>

        <div className="side-foot">
          <button className="nav-item" onClick={() => setSettingsOpen(true)}>
            <GearIcon />
            <span>{t('Settings')}</span>
          </button>
          <button className="nav-item" onClick={signOut}>
            <span className="glyph">→</span>
            <span>{t('Sign out')}</span>
          </button>
        </div>
      </aside>

      <main className="main">
        <header className="top">
          {detail ? (
            <button className="back" onClick={() => navigate('/')}>
              ← {t('Back')}
            </button>
          ) : (
            <div className="brand">
              <FilmIcon />
              <span>{t('Codie Video')}</span>
            </div>
          )}
        </header>
        {detail ? (
          <DetailPage src={decodeURIComponent(detail[1])} id={decodeURIComponent(detail[2])} />
        ) : (
          <BrowsePage
            src={src}
            tab={tab}
            tabLabel={tabLabel}
            tabsErr={tabsErr}
            query={searching ? query : ''}
            sources={sources}
            onReloadSources={reloadSources}
          />
        )}
      </main>

      {settingsOpen && (
        <SettingsDialog
          onClose={() => setSettingsOpen(false)}
          onChanged={() => {
            reloadSources()
            setTab('')
          }}
        />
      )}
    </div>
  )
}

function readRecent() {
  try {
    const v = JSON.parse(localStorage.getItem('cvp-recent') || '[]')
    return Array.isArray(v) ? v.filter((s) => typeof s === 'string') : []
  } catch {
    return []
  }
}

function NavGlyph({ id }) {
  if (id === 'hot') return <span className="glyph">▲</span>
  if (id === 'rank') return <span className="glyph">#</span>
  if (id === 'watchlater') return <span className="glyph">○</span>
  if (id === 'add' || id.startsWith('dir:') || id.startsWith('season:')) return <span className="glyph">□</span>
  if (id.startsWith('fav:')) return <span className="glyph">♡</span>
  return <span className="glyph">■</span>
}

function SearchIcon() {
  return (
    <svg className="search-icon" viewBox="0 0 24 24" width="14" height="14" aria-hidden="true">
      <circle cx="11" cy="11" r="6.5" fill="none" stroke="currentColor" strokeWidth="2" />
      <path d="M16 16l5 5" stroke="currentColor" strokeWidth="2" strokeLinecap="round" />
    </svg>
  )
}
function FilmIcon() {
  return (
    <svg viewBox="0 0 24 24" width="16" height="16" aria-hidden="true">
      <rect x="3" y="5" width="18" height="14" rx="2" fill="none" stroke="currentColor" strokeWidth="2" />
      <path d="M7 5v14M17 5v14M3 9h4M3 15h4M17 9h4M17 15h4" stroke="currentColor" strokeWidth="2" />
    </svg>
  )
}
function GearIcon() {
  return (
    <svg viewBox="0 0 24 24" width="15" height="15" aria-hidden="true">
      <circle cx="12" cy="12" r="3" fill="none" stroke="currentColor" strokeWidth="2" />
      <path
        d="M12 3.5v2.2M12 18.3V20.5M3.5 12h2.2M18.3 12H20.5M5.8 5.8l1.6 1.6M16.6 16.6l1.6 1.6M18.2 5.8l-1.6 1.6M7.4 16.6l-1.6 1.6"
        stroke="currentColor"
        strokeWidth="2"
        strokeLinecap="round"
      />
    </svg>
  )
}
