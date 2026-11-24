import { useEffect, useState } from 'react'
import { get, post, coverUrl } from '../api.js'
import { t } from '../i18n.js'
import { navigate } from '../App.jsx'

// 详情页：海报、简介、追剧按钮、一集一个按钮。有进度的一集带"继续"。
export default function DetailPage({ src, id }) {
  const [data, setData] = useState(null)
  const [err, setErr] = useState('')
  const [followed, setFollowed] = useState(false)

  const load = () => {
    get(`/api/video/${src}/${encodeURIComponent(id)}`)
      .then((d) => {
        setData(d)
        setFollowed(!!d.followed)
      })
      .catch((e) => setErr(e.message))
  }
  useEffect(load, [src, id])

  if (err) {
    return (
      <div className="page-center">
        <div className="hint">{err}</div>
        <button className="btn" onClick={load}>
          {t('retry')}
        </button>
      </div>
    )
  }
  if (!data) return <div className="page-center hint">{t('loading')}</div>

  const v = data.video
  const prog = data.progress
  const firstUnfinished = prog && !prog.done && (prog.epAt > 0 || prog.pos > 0) ? prog.epAt : 0

  return (
    <div className="shell">
      <header className="topbar">
        <button className="chip" onClick={() => navigate('/')}>
          ← {t('back')}
        </button>
      </header>
      <div className="detail">
        <div className="detail-poster">
          <img src={coverUrl(v.cover)} alt="" />
        </div>
        <div className="detail-info">
          <h1>{v.title}</h1>
          <div className="detail-meta">
            {v.year > 0 && <span>{v.year}</span>}
            {v.score && <span>评分 {v.score}</span>}
            {v.finished && <span>完结</span>}
            <span>共 {v.episodes?.length || 0} 集</span>
          </div>
          {v.intro && <p className="detail-intro">{v.intro}</p>}
          <div className="detail-actions">
            <button
              className="btn btn-accent"
              onClick={() =>
                navigate(
                  `/watch/${src}/${encodeURIComponent(id)}/${encodeURIComponent(
                    v.episodes[firstUnfinished]?.id || v.episodes?.[0]?.id || '',
                  )}`,
                )
              }
            >
              {prog && prog.pos > 10 && !prog.done
                ? `▶ ${t('resume', prog.pos)}`
                : `▶ ${t('play')}`}
            </button>
            <button
              className={'chip' + (followed ? ' chip-on' : '')}
              onClick={async () => {
                const d = await post('/api/follow', { item: { id: v.id, source: v.source, title: v.title, cover: v.cover } })
                setFollowed(!!d.followed)
              }}
            >
              {followed ? t('followingOn') : t('follow')}
            </button>
          </div>
        </div>
      </div>
      <div className="episodes">
        {(v.episodes || []).map((ep, i) => (
          <button
            key={ep.id}
            className={'ep-btn' + (prog && prog.epAt === i ? ' ep-on' : '')}
            disabled={ep.unavailable}
            onClick={() => navigate(`/watch/${src}/${encodeURIComponent(id)}/${encodeURIComponent(ep.id)}`)}
          >
            {ep.title || t('ep', i + 1)}
          </button>
        ))}
      </div>
    </div>
  )
}
