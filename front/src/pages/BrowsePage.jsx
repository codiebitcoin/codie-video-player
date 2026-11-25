import { useEffect, useMemo, useState } from 'react'
import { coverUrl, get, isPlaylistTab, post, Unauthorized } from '../api.js'
import { t } from '../i18n.js'
import { navigate } from '../App.jsx'

export default function BrowsePage({ src, tab, tabLabel, tabsErr, query, onReloadSources }) {
  if (query) return <SearchView query={query} />
  if (!src) return <div className="hint pad">{t('Loading')}</div>
  if (tab === 'add') return <AddFolder onAdded={onReloadSources} />
  return <Library src={src} tab={tab} tabLabel={tabLabel} tabsErr={tabsErr} />
}

function Library({ src, tab, tabLabel, tabsErr }) {
  const [items, setItems] = useState([])
  const [page, setPage] = useState(1)
  const [more, setMore] = useState(false)
  const [loading, setLoading] = useState(true)
  const [err, setErr] = useState('')
  const [filter, setFilter] = useState('')
  const playlist = isPlaylistTab(tab)

  useEffect(() => {
    setFilter('')
    if (!src || !tab) return
    let live = true
    setLoading(true)
    setErr('')
    setPage(1)
    setItems([])
    get(`/api/source/${encodeURIComponent(src)}/list?tab=${encodeURIComponent(tab)}&page=1`)
      .then((d) => {
        if (!live) return
        setItems(d.items || [])
        setMore(!!d.more)
        setErr(d.error || '')
      })
      .catch((e) => live && !(e instanceof Unauthorized) && setErr(e.message))
      .finally(() => live && setLoading(false))
    return () => {
      live = false
    }
  }, [src, tab])

  const loadMore = async () => {
    const next = page + 1
    setPage(next)
    setLoading(true)
    try {
      const d = await get(
        `/api/source/${encodeURIComponent(src)}/list?tab=${encodeURIComponent(tab)}&page=${next}`,
      )
      setItems((old) => [...old, ...(d.items || [])])
      setMore(!!d.more)
    } catch (e) {
      if (!(e instanceof Unauthorized)) setErr(e.message)
    } finally {
      setLoading(false)
    }
  }

  const shown = useMemo(() => {
    const q = filter.trim().toLowerCase()
    if (!q) return items
    return items.filter((it) => (it.title || '').toLowerCase().includes(q) || (it.subtitle || '').toLowerCase().includes(q))
  }, [items, filter])

  const playAt = (list, index, shuffle) => {
    const queue = shuffle ? shuffled(list) : list
    const start = shuffle ? 0 : index
    rememberQueue(queue, src)
    const it = queue[start]
    if (it) openWatch(it.source || src, it.id, firstEp(it))
  }

  return (
    <div className="browse">
      {!playlist && <Shelves src={src} />}
      {playlist && (
        <div className="pl-head">
          <div className="pl-art">{items[0]?.cover ? <img src={coverUrl(items[0].cover)} alt="" /> : <span>□</span>}</div>
          <div>
            <div className="pl-kicker">{t('Playlist')}</div>
            <h1>{tabLabel}</h1>
            {!loading && <div className="hint">{t('%d videos', shown.length)}</div>}
            <div className="pl-actions">
              <button className="btn accent" disabled={!shown.length} onClick={() => playAt(shown, 0, false)}>
                ▶ {t('Play')}
              </button>
              <button className="btn" disabled={shown.length < 2} onClick={() => playAt(shown, 0, true)}>
                {t('Shuffle')}
              </button>
              <input
                className="field grow"
                placeholder={t('Search in playlist…')}
                value={filter}
                onChange={(e) => setFilter(e.target.value)}
              />
            </div>
          </div>
        </div>
      )}
      {(err || tabsErr) && <div className="grid-err">{err || tabsErr}</div>}
      {loading && items.length === 0 ? (
        <Skeleton />
      ) : items.length === 0 && tab ? (
        <div className="hint pad">
          {src === 'local' && tab === 'add' ? t('Add a folder of videos to play it here') : t('Nothing here yet')}
        </div>
      ) : shown.length === 0 ? (
        <div className="hint pad">{t('Nothing found for “%s”', filter.trim())}</div>
      ) : (
        <div className="grid">
          {shown.map((it, i) => (
            <Card
              key={(it.id || '') + i}
              item={it}
              onPlay={() => {
                if (playlist) playAt(shown, i, false)
                else {
                  rememberQueue([], src)
                  openWatch(it.source || src, it.id, firstEp(it))
                }
              }}
            />
          ))}
        </div>
      )}
      {more && !filter && (
        <div className="more">
          <button className="btn" onClick={loadMore} disabled={loading}>
            {loading ? t('Loading') : t('Load More')}
          </button>
        </div>
      )}
    </div>
  )
}

function Shelves({ src }) {
  return (
    <>
      <Shelf src={src} kind="history" title={t('Continue Watching')} />
      <Shelf src={src} kind="follows" title={t('Following')} />
    </>
  )
}

function Shelf({ src, kind, title }) {
  const [items, setItems] = useState([])
  useEffect(() => {
    if (!src) return
    get(`/api/${kind}?src=${encodeURIComponent(src)}`)
      .then((d) => setItems((d[kind] || []).slice(0, 12)))
      .catch(() => setItems([]))
  }, [src, kind])
  if (!items.length) return null
  return (
    <section className="shelf">
      <h2>
        {title} <span>{items.length}</span>
      </h2>
      <div className="shelf-row">
        {items.map((e) => (
          <button
            key={e.id}
            className="card shelf-card"
            onClick={() => {
              if (e.epId) openWatch(e.source || src, e.id, e.epId)
              else navigate(`/detail/${encodeURIComponent(e.source || src)}/${encodeURIComponent(e.id)}`)
            }}
          >
            <div className="poster">
              {e.cover ? <img src={coverUrl(e.cover)} alt="" /> : <span className="ph">■</span>}
              {caption(e) && <span className="badge">{caption(e)}</span>}
            </div>
            <div className="card-title">{e.title}</div>
            <div className="card-sub">{caption(e)}</div>
          </button>
        ))}
      </div>
    </section>
  )
}

function caption(e) {
  if (e.done) return t('Finished')
  if (!e.epAt && !e.pos && !e.epId) return ''
  let s = t('Ep %d', (e.epAt || 0) + 1)
  if (e.ep) s += ' · ' + e.ep
  else if (e.dur > 0 && e.pos > 0) s += ' · ' + Math.round((e.pos / e.dur) * 100) + '%'
  return s
}

export function Card({ item, onPlay }) {
  const src = item.source || ''
  const id = item.id
  return (
    <div className="card">
      <button className="poster-btn" onClick={() => navigate(`/detail/${encodeURIComponent(src)}/${encodeURIComponent(id)}`)}>
        <div className="poster">
          {item.cover ? <img loading="lazy" src={coverUrl(item.cover)} alt="" /> : <span className="ph">■</span>}
          {item.subtitle && <span className="badge">{item.subtitle}</span>}
        </div>
        <div className="card-title">{item.title}</div>
        {item.subtitle && <div className="card-sub">{item.subtitle}</div>}
      </button>
      {onPlay && (
        <button className="play-fab" aria-label={t('Play')} onClick={onPlay}>
          ▶
        </button>
      )}
    </div>
  )
}

function SearchView({ query }) {
  const [groups, setGroups] = useState(null)
  const [filter, setFilter] = useState('all')
  useEffect(() => {
    let live = true
    setGroups(null)
    setFilter('all')
    get(`/api/search?q=${encodeURIComponent(query)}`)
      .then((d) => live && setGroups(d.groups || []))
      .catch(() => live && setGroups([]))
    return () => {
      live = false
    }
  }, [query])
  if (!groups) return <div className="hint pad">{t('Loading')}</div>
  const hits = groups.filter((g) => g.items?.length)
  const shown = filter === 'all' ? hits : hits.filter((g) => g.source === filter)
  const problems = groups.map((g) => (g.error ? `${g.name}: ${g.error}` : '')).filter(Boolean)
  return (
    <div className="browse">
      {hits.length > 1 && (
        <div className="chips">
          <button className={filter === 'all' ? 'on' : ''} onClick={() => setFilter('all')}>
            {t('All')} {hits.reduce((n, g) => n + g.items.length, 0)}
          </button>
          {hits.map((g) => (
            <button key={g.source} className={filter === g.source ? 'on' : ''} onClick={() => setFilter(g.source)}>
              {g.name} {g.items.length}
            </button>
          ))}
        </div>
      )}
      {shown.length === 0 && <div className="hint pad">{t('Nothing found for “%s”', query)}</div>}
      {shown.map((g) => (
        <section key={g.source} className="shelf">
          <h2>{g.name}</h2>
          <div className="grid">
            {g.items.map((it, i) => (
              <Card key={(it.id || '') + i} item={{ ...it, source: it.source || g.source }} />
            ))}
          </div>
        </section>
      ))}
      {problems.length > 0 && <div className="hint pad">{problems.join(' · ')}</div>}
    </div>
  )
}

function AddFolder({ onAdded }) {
  const [path, setPath] = useState('')
  const [err, setErr] = useState('')
  const [busy, setBusy] = useState(false)
  return (
    <form
      className="add-folder"
      onSubmit={async (e) => {
        e.preventDefault()
        setBusy(true)
        setErr('')
        try {
          await post('/api/local/folder', { path: path.trim() })
          onAdded?.()
        } catch (ex) {
          setErr(ex.message)
        } finally {
          setBusy(false)
        }
      }}
    >
      <h1>{t('Add Folder')}</h1>
      <p className="hint">{t('Add a folder of videos to play it here')}</p>
      <input
        className="field"
        placeholder={t('Folder path on the server')}
        value={path}
        onChange={(e) => setPath(e.target.value)}
      />
      {err && <div className="grid-err">{err}</div>}
      <button className="btn accent" disabled={busy || !path.trim()}>
        {t('Add')}
      </button>
    </form>
  )
}

function Skeleton() {
  return (
    <div className="grid">
      {Array.from({ length: 12 }, (_, i) => (
        <div key={i} className="card sk">
          <div className="poster" />
          <div className="sk-line" />
          <div className="sk-line short" />
        </div>
      ))}
    </div>
  )
}

function firstEp(item) {
  if ((item.source || '') === 'huangguo') return '1'
  return item.id
}

function openWatch(src, id, ep) {
  navigate(`/watch/${encodeURIComponent(src)}/${encodeURIComponent(id)}/${encodeURIComponent(ep || id)}`)
}

function rememberQueue(items, src) {
  const queue = items.map((it) => ({
    source: it.source || src,
    id: it.id,
    ep: (it.source || src) === 'huangguo' ? '1' : it.id,
    title: it.title,
  }))
  sessionStorage.setItem('cvp-queue', JSON.stringify(queue))
}

function shuffled(list) {
  const out = list.slice()
  for (let i = out.length - 1; i > 0; i--) {
    const j = Math.floor(Math.random() * (i + 1))
    ;[out[i], out[j]] = [out[j], out[i]]
  }
  return out
}
