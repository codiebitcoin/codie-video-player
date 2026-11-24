import { useEffect, useRef, useState } from 'react'
import { get, post, coverUrl, Unauthorized } from '../api.js'
import { t } from '../i18n.js'
import { navigate } from '../App.jsx'

// 首页 = 顶栏（源 + 搜索框）+ 继续观看/追剧架 + 当前源的分类和网格 + 搜索结果。
export default function HomePage({ route }) {
  const [sources, setSources] = useState([])
  const [src, setSrc] = useState('')
  const [q, setQ] = useState(new URLSearchParams(window.location.search).get('q') || '')
  const [searching, setSearching] = useState(false)
  const searchingRef = useRef(false)

  useEffect(() => {
    get('/api/sources').then((d) => {
      setSources(d.sources || [])
      setSrc((s) => s || d.sources?.[0]?.id || '')
    })
  }, [])

  // 路径带 ?q= 时是搜索视图。
  useEffect(() => {
    const params = new URLSearchParams(window.location.search)
    const query = params.get('q') || ''
    setQ(query)
    setSearching(!!query)
    searchingRef.current = !!query
  }, [route])

  const doSearch = (e) => {
    e.preventDefault()
    if (q.trim()) {
      navigate(`/?q=${encodeURIComponent(q.trim())}`)
    }
  }

  return (
    <div className="shell">
      <header className="topbar">
        <span className="logo">🎬 {t('appName')}</span>
        <div className="source-picker">
          {sources.map((s) => (
            <button
              key={s.id}
              className={'chip' + (!searching && s.id === src ? ' chip-on' : '')}
              onClick={() => {
                setSearching(false)
                searchingRef.current = false
                setSrc(s.id)
                navigate('/')
              }}
            >
              {s.name}
            </button>
          ))}
        </div>
        <form className="searchbox" onSubmit={doSearch}>
          <input
            className="input"
            placeholder={t('searchPlaceholder')}
            value={q}
            onChange={(e) => setQ(e.target.value)}
          />
        </form>
        <button className="chip" onClick={() => navigate('/list')}>
          {t('lists')}
        </button>
        <button
          className="chip"
          onClick={async () => {
            try {
              await post('/api/logout')
            } catch {}
            window.location.href = '/login'
          }}
        >
          {t('logout')}
        </button>
      </header>

      {searching ? (
        <SearchView query={q} />
      ) : (
        <>
          <Shelf kind="history" title={t('continueWatching')} />
          <Shelf kind="follows" title={t('following')} />
          <Grid srcId={src} />
        </>
      )}
    </div>
  )
}

// Shelf 是一行横排卡片：继续观看或追剧。
function Shelf({ kind, title }) {
  const [items, setItems] = useState([])
  useEffect(() => {
    get(`/api/${kind}`)
      .then((d) => setItems(d[kind] || []))
      .catch(() => {})
  }, [kind])
  if (!items.length) return null
  return (
    <section className="shelf">
      <h2 className="shelf-title">
        <button className="shelf-link" onClick={() => navigate(`/list?tab=${kind}`)}>
          {title} ›
        </button>
      </h2>
      <div className="shelf-row">
        {items.map((e) => (
          <button
            key={e.ID || e.id}
            className="card"
            onClick={() =>
              navigate(`/detail/${e.Source || e.source}/${encodeURIComponent(e.ID || e.id)}`)
            }
          >
            <div className="poster">
              <img loading="lazy" src={coverUrl(e.Cover || e.cover)} alt="" />
              {caption(e) && <span className="badge">{caption(e)}</span>}
            </div>
            <div className="card-title">{e.Title || e.title}</div>
          </button>
        ))}
      </div>
    </section>
  )
}

// caption 说一条记录看到哪了：第几集和百分比；还没开播的（追剧）就没有。
export function caption(e) {
  // shelf 条目和 video.Item 的字段大小写不同，都兼容。
  const epAt = e.EpAt ?? e.epAt ?? 0
  const pos = e.Pos ?? e.pos ?? 0
  const dur = e.Dur ?? e.dur ?? 0
  if (epAt === 0 && pos === 0) return ''
  let s = t('ep', epAt + 1)
  if (dur > 0 && pos > 0) s += ' · ' + Math.round((pos / dur) * 100) + '%'
  return s
}

// Grid 是一个源的分类 + 海报网格，带加载更多。
function Grid({ srcId }) {
  const [tabs, setTabs] = useState([])
  const [tab, setTab] = useState('')
  const [items, setItems] = useState([])
  const [page, setPage] = useState(1)
  const [more, setMore] = useState(false)
  const [loading, setLoading] = useState(true)
  const [err, setErr] = useState('')

  useEffect(() => {
    if (!srcId) return
    setTabs([])
    setTab('')
    setItems([])
    setLoading(true)
    get(`/api/source/${srcId}/tabs`)
      .then((d) => {
        setTabs(d.tabs || [])
        setTab(d.tabs?.[0]?.id || '')
      })
      .catch((e) => {
        if (!(e instanceof Unauthorized)) setErr(e.message)
        setLoading(false)
      })
  }, [srcId])

  useEffect(() => {
    if (!srcId || !tab) return
    setPage(1)
    setItems([])
    setLoading(true)
    setErr('')
    load(srcId, tab, 1, setItems, setMore, setLoading, setErr)
  }, [srcId, tab])

  const loadMore = () => {
    const next = page + 1
    setPage(next)
    load(srcId, tab, next, (n) => setItems((old) => [...old, ...n]), setMore, setLoading, setErr)
  }

  return (
    <section className="grid-area">
      {tabs.length > 0 && (
        <div className="tabs">
          {tabs.map((tb) => (
            <button
              key={tb.id}
              className={'chip' + (tb.id === tab ? ' chip-on' : '')}
              onClick={() => setTab(tb.id)}
            >
              {tb.label}
            </button>
          ))}
        </div>
      )}
      {err && <div className="grid-err">{err}</div>}
      {items.length === 0 && loading && <div className="hint">{t('loading')}</div>}
      <div className="grid">
        {items.map((it, i) => (
          <Poster key={it.ID + i} item={it} />
        ))}
      </div>
      {more && (
        <div className="grid-more">
          <button className="btn" onClick={loadMore} disabled={loading}>
            {loading ? t('loading') : t('loadMore')}
          </button>
        </div>
      )}
    </section>
  )
}

async function load(srcId, tab, page, setItems, setMore, setLoading, setErr) {
  try {
    const d = await get(`/api/source/${srcId}/list?tab=${encodeURIComponent(tab)}&page=${page}`)
    setItems(d.items || [])
    setMore(!!d.more)
  } catch (e) {
    if (!(e instanceof Unauthorized)) setErr(e.message)
  } finally {
    setLoading(false)
  }
}

export function Poster({ item }) {
  const id = item.ID || item.id
  const src = item.Source || item.source || ''
  return (
    <button className="card" onClick={() => navigate(`/detail/${src}/${encodeURIComponent(id)}`)}>
      <div className="poster">
        <img loading="lazy" src={coverUrl(item.Cover || item.cover)} alt="" />
      </div>
      <div className="card-title">{item.Title || item.title}</div>
    </button>
  )
}

// SearchView 把各源的结果一组一组摆出来。
function SearchView({ query }) {
  const [groups, setGroups] = useState(null)
  useEffect(() => {
    let live = true
    setGroups(null)
    get(`/api/search?q=${encodeURIComponent(query)}`)
      .then((d) => live && setGroups(d.groups || []))
      .catch(() => live && setGroups([]))
    return () => {
      live = false
    }
  }, [query])

  if (!groups) return <div className="hint page-center">{t('loading')}</div>
  const hasResults = groups.some((g) => g.items?.length)
  return (
    <div className="grid-area">
      {!hasResults && <div className="hint page-center">{t('noResults')}「{query}」</div>}
      {groups
        .filter((g) => g.items?.length)
        .map((g) => (
          <section key={g.source} className="shelf">
            <h2 className="shelf-title">{g.name}</h2>
            {g.error && <div className="grid-err">{g.error}</div>}
            <div className="shelf-row">
              {g.items.map((it) => (
                <Poster key={it.ID} item={it} />
              ))}
            </div>
          </section>
        ))}
    </div>
  )
}
