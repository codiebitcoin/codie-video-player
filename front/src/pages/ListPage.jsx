import { useEffect, useState } from 'react'
import { get, coverUrl } from '../api.js'
import { t } from '../i18n.js'
import { navigate } from '../App.jsx'
import { caption } from './HomePage.jsx'

// 列表页：观看历史和追剧的完整清单（首页的架子只露一行），带一个框搜索列表。
export default function ListPage() {
  const [tab, setTab] = useState(
    new URLSearchParams(window.location.search).get('tab') === 'follows' ? 'follows' : 'history',
  )
  const [lists, setLists] = useState({ history: null, follows: null })
  const [q, setQ] = useState('')

  useEffect(() => {
    get('/api/history')
      .then((d) => setLists((s) => ({ ...s, history: d.history || [] })))
      .catch(() => setLists((s) => ({ ...s, history: [] })))
    get('/api/follows')
      .then((d) => setLists((s) => ({ ...s, follows: d.follows || [] })))
      .catch(() => setLists((s) => ({ ...s, follows: [] })))
  }, [])

  const items = lists[tab] || []
  const query = q.trim().toLowerCase()
  const shown = query
    ? items.filter((e) => (e.Title || e.title || '').toLowerCase().includes(query))
    : items

  return (
    <div className="shell">
      <header className="topbar">
        <button className="chip" onClick={() => navigate('/')}>
          ← {t('back')}
        </button>
        <span className="logo">{t('lists')}</span>
        <form className="searchbox" onSubmit={(e) => e.preventDefault()}>
          <input
            className="input"
            placeholder={t('searchList')}
            value={q}
            onChange={(e) => setQ(e.target.value)}
          />
        </form>
      </header>

      <div className="tabs">
        <button
          className={'chip' + (tab === 'history' ? ' chip-on' : '')}
          onClick={() => setTab('history')}
        >
          {t('history')}
        </button>
        <button
          className={'chip' + (tab === 'follows' ? ' chip-on' : '')}
          onClick={() => setTab('follows')}
        >
          {t('following')}
        </button>
      </div>

      {lists[tab] === null ? (
        <div className="hint page-center">{t('loading')}</div>
      ) : items.length === 0 ? (
        <div className="hint page-center">{tab === 'history' ? t('noHistory') : t('noFollows')}</div>
      ) : shown.length === 0 ? (
        <div className="hint page-center">{t('noMatch', q.trim())}</div>
      ) : (
        <div className="grid-area">
          <div className="grid">
            {shown.map((e) => (
              <ListCard
                key={(e.Source || e.source || '') + (e.ID || e.id)}
                e={e}
              />
            ))}
          </div>
        </div>
      )}
    </div>
  )
}

// ListCard 是一条记录：封面、看到哪了（没有进度的追剧不戴徽章）、剧名。
function ListCard({ e }) {
  const src = e.Source || e.source || ''
  const id = e.ID || e.id
  const cap = caption(e)
  return (
    <button
      className="card"
      onClick={() => navigate(`/detail/${src}/${encodeURIComponent(id)}`)}
    >
      <div className="poster">
        <img loading="lazy" src={coverUrl(e.Cover || e.cover)} alt="" />
        {cap && <span className="badge">{cap}</span>}
      </div>
      <div className="card-title">{e.Title || e.title}</div>
    </button>
  )
}
