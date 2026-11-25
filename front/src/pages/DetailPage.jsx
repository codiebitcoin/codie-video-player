import { useEffect, useState } from 'react'
import { coverUrl, get, post } from '../api.js'
import { t } from '../i18n.js'
import { navigate } from '../App.jsx'

export default function DetailPage({ src, id }) {
  const [data, setData] = useState(null)
  const [err, setErr] = useState('')
  const [followed, setFollowed] = useState(false)

  const load = () => {
    setErr('')
    setData(null)
    get(`/api/video/${encodeURIComponent(src)}/${encodeURIComponent(id)}`)
      .then((d) => {
        setData(d)
        setFollowed(!!d.followed)
      })
      .catch((e) => setErr(e.message))
  }
  useEffect(load, [src, id])

  if (err) {
    return (
      <div className="hint pad">
        <div>{err}</div>
        <button className="btn" onClick={load}>
          {t('Retry')}
        </button>
      </div>
    )
  }
  if (!data?.video) return <div className="hint pad">{t('Loading')}</div>

  const v = data.video
  const eps = v.episodes || []
  const prog = data.progress
  let playAt = 0
  let label = eps.length > 1 ? t('Play Episode 1') : t('Play')
  if (prog && !prog.done && prog.pos > 10) {
    const i = eps.findIndex((e) => e.id === prog.epId)
    if (i >= 0) playAt = i
    label = t('Continue · Ep %d', playAt + 1)
  }
  const meta = [v.subtitle, v.year > 0 ? String(v.year) : '', v.finished ? t('Completed') : eps.length ? t('Ongoing') : '']
    .filter(Boolean)
    .join(' · ')

  const watch = (ep) => {
    if (!ep) return
    navigate(`/watch/${encodeURIComponent(src)}/${encodeURIComponent(id)}/${encodeURIComponent(ep.id)}`)
  }

  return (
    <div className="detail-page">
      <div className="detail">
        <div className="detail-poster">{v.cover ? <img src={coverUrl(v.cover)} alt="" /> : <span className="ph">■</span>}</div>
        <div className="detail-info">
          <h1>{v.title}</h1>
          {meta && <div className="detail-meta">{meta}</div>}
          <div className="detail-actions">
            {v.score && <span className="score">{t('Score %s', v.score)}</span>}
            <button
              className={'chip follow' + (followed ? ' on' : '')}
              onClick={async () => {
                const d = await post('/api/follow', {
                  item: { id: v.id, source: src, title: v.title, cover: v.cover, subtitle: v.subtitle, year: v.year },
                })
                setFollowed(!!d.followed)
              }}
            >
              {followed ? t('Following') : t('Follow')}
            </button>
          </div>
          {v.intro && <p className="detail-intro">{v.intro}</p>}
          {eps.length > 0 && (
            <button className="btn accent" onClick={() => watch(eps[playAt] || eps[0])}>
              ▶ {label}
            </button>
          )}
        </div>
      </div>

      <div className="ep-head">
        <h2>{t('Episodes')}</h2>
        <span className="hint">{t('%d episodes', eps.length)}</span>
      </div>
      {eps.length === 0 ? (
        <div className="hint pad">{t('No playable episodes for this drama')}</div>
      ) : eps.length > 60 ? (
        <div className="ep-list">
          {eps.map((ep, i) => (
            <button key={ep.id + i} className={prog?.epId === ep.id ? 'on' : ''} disabled={ep.unavailable} onClick={() => watch(ep)}>
              <span>
                {i + 1} {ep.title || t('Ep %d', i + 1)}
              </span>
              {ep.seconds > 0 && <em>{clock(ep.seconds)}</em>}
            </button>
          ))}
        </div>
      ) : (
        <div className="episodes">
          {eps.map((ep, i) => (
            <button key={ep.id + i} className={'ep-btn' + (prog?.epId === ep.id ? ' on' : '')} disabled={ep.unavailable} onClick={() => watch(ep)}>
              <span>{ep.title || t('Ep %d', i + 1)}</span>
              {prog?.epId === ep.id && prog.pos > 10 && !prog.done ? (
                <em>{clock(prog.pos)}</em>
              ) : ep.seconds > 0 ? (
                <em>{clock(ep.seconds)}</em>
              ) : null}
            </button>
          ))}
        </div>
      )}
    </div>
  )
}

function clock(sec) {
  sec = Math.max(0, Math.round(sec))
  const m = Math.floor(sec / 60)
  const s = sec % 60
  return `${m}:${String(s).padStart(2, '0')}`
}
