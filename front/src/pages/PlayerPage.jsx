import Hls from 'hls.js'
import { useEffect, useRef, useState } from 'react'
import { del, get, post } from '../api.js'
import { t } from '../i18n.js'
import { navigate } from '../App.jsx'

const rates = [0.5, 0.75, 1, 1.25, 1.5, 2]

export default function PlayerPage({ src, id, ep }) {
  const [detail, setDetail] = useState(null)
  const [session, setSession] = useState(null)
  const [rate, setRate] = useState(1)
  const [nextLeft, setNextLeft] = useState(0)
  const [fatal, setFatal] = useState('')
  const [playing, setPlaying] = useState(false)
  const videoRef = useRef(null)
  const startedAt = useRef(false)

  const eps = detail?.video?.episodes || []
  const index = eps.findIndex((e) => e.id === ep)
  const prev = index > 0 ? eps[index - 1] : null
  const nextEp = index >= 0 && index + 1 < eps.length ? eps[index + 1] : null

  useEffect(() => {
    get(`/api/video/${encodeURIComponent(src)}/${encodeURIComponent(id)}`)
      .then(setDetail)
      .catch((e) => setFatal(e.message))
  }, [src, id])

  useEffect(() => {
    startedAt.current = false
    setFatal('')
    setPlaying(false)
    let live = true
    let sessId = ''
    post('/api/play', { source: src, item: id, ep })
      .then((d) => {
        if (!live) {
          if (d?.id) del(`/api/session/${d.id}`).catch(() => {})
          return
        }
        sessId = d.id
        setSession(d)
        if (d.error && d.state === 'error') setFatal(d.error)
      })
      .catch((e) => live && setFatal(e.message))
    return () => {
      live = false
      if (sessId) del(`/api/session/${sessId}`).catch(() => {})
      setSession(null)
    }
  }, [src, id, ep])

  useEffect(() => {
    if (!session?.id || fatal) return
    const video = videoRef.current
    if (!video) return
    let hls = null
    let wait = null
    let stopped = false

    const attach = () => {
      if (stopped || video.dataset.attached) return
      video.dataset.attached = '1'
      const url = `/api/session/${session.id}/index.m3u8`
      if (video.canPlayType('application/vnd.apple.mpegurl')) {
        video.src = url
      } else if (Hls.isSupported()) {
        hls = new Hls({ liveDurationInfinity: true, backBufferLength: 90 })
        hls.loadSource(url)
        hls.attachMedia(video)
        hls.on(Hls.Events.ERROR, (_, data) => {
          if (data.fatal) setFatal(t('Playback failed. Try again.'))
        })
      } else {
        setFatal(t('This browser can’t play this stream'))
      }
    }

    const poll = async () => {
      const st = await get(`/api/session/${session.id}`).catch(() => null)
      if (stopped || !st) return
      setSession((old) => (old ? { ...old, state: st.state, error: st.error } : old))
      if (st.state === 'ready') attach()
      if (st.state === 'error') setFatal(st.error || t('Playback failed. Try again.'))
      else wait = setTimeout(poll, st.state === 'ready' ? 4000 : 800)
    }
    poll()

    return () => {
      stopped = true
      if (wait) clearTimeout(wait)
      if (hls) hls.destroy()
      delete video.dataset.attached
    }
  }, [session?.id])

  useEffect(() => {
    const video = videoRef.current
    if (!video || !detail?.progress) return
    const p = detail.progress
    if (p.epId !== ep || p.done || !(p.pos > 10)) return
    const onMeta = () => {
      if (!startedAt.current) {
        video.currentTime = Math.max(0, p.pos - 2)
        startedAt.current = true
      }
    }
    video.addEventListener('loadedmetadata', onMeta)
    return () => video.removeEventListener('loadedmetadata', onMeta)
  }, [detail, ep, session?.id])

  useEffect(() => {
    const video = videoRef.current
    if (!video || !session?.id) return
    const report = (done) => {
      post(`/api/session/${session.id}/progress`, {
        pos: video.currentTime || 0,
        dur: Number.isFinite(video.duration) ? video.duration : 0,
        done,
      }).catch(() => {})
    }
    const timer = setInterval(() => {
      if (video.currentTime > 0 && !video.paused) report(false)
    }, 10000)
    const onEnded = () => {
      report(true)
      if (nextEp || queueNext()) setNextLeft(8)
    }
    video.addEventListener('ended', onEnded)
    return () => {
      clearInterval(timer)
      video.removeEventListener('ended', onEnded)
      if (video.currentTime > 0) report(false)
    }
  }, [session?.id, nextEp, id])

  useEffect(() => {
    if (nextLeft <= 0) return
    const timer = setTimeout(() => {
      if (nextLeft <= 1) {
        setNextLeft(0)
        goNext()
      } else setNextLeft(nextLeft - 1)
    }, 1000)
    return () => clearTimeout(timer)
  }, [nextLeft])

  const go = (episode) => {
    if (!episode) return
    setNextLeft(0)
    navigate(`/watch/${encodeURIComponent(src)}/${encodeURIComponent(id)}/${encodeURIComponent(episode.id)}`)
  }

  const goNext = () => {
    if (nextEp) go(nextEp)
    else {
      const n = queueNext()
      if (n) navigate(`/watch/${encodeURIComponent(n.source)}/${encodeURIComponent(n.id)}/${encodeURIComponent(n.ep || n.id)}`)
    }
  }

  const changeRate = (r) => {
    setRate(r)
    if (videoRef.current) videoRef.current.playbackRate = r
  }

  const waiting = !fatal && !playing && (!session || session.state === 'queued' || session.state === 'making')

  return (
    <div className="player-page">
      <div className="player-top">
        <button className="back" onClick={() => navigate(`/detail/${encodeURIComponent(src)}/${encodeURIComponent(id)}`)}>
          ← {detail?.video?.title || t('Back')}
        </button>
        <span className="player-ep">{eps[index]?.title || (index >= 0 ? t('Ep %d', index + 1) : '')}</span>
        <div className="player-rate" aria-label={t('Speed')}>
          {rates.map((r) => (
            <button key={r} className={r === rate ? 'on' : ''} onClick={() => changeRate(r)}>
              {r}×
            </button>
          ))}
        </div>
      </div>

      <div className="stage">
        <video
          ref={videoRef}
          controls
          autoPlay
          playsInline
          onPlay={() => setPlaying(true)}
          onPlaying={() => setPlaying(true)}
        />
        {waiting && (
          <div className="stage-overlay">
            {session?.state === 'making' || session?.state === 'ready' ? t('Transcoding — the picture is on its way…') : t('Getting the stream…')}
          </div>
        )}
        {fatal && (
          <div className="stage-overlay">
            <div>{fatal}</div>
            <button className="btn" onClick={() => window.location.reload()}>
              {t('Retry')}
            </button>
          </div>
        )}
        {nextLeft > 0 && (
          <div className="upnext">
            <span>{t('Up next in %d s', nextLeft)}</span>
            <button className="btn accent" onClick={goNext}>
              {t('Play now')}
            </button>
            <button className="btn" onClick={() => setNextLeft(0)}>
              {t('Cancel')}
            </button>
          </div>
        )}
      </div>

      <div className="player-controls">
        <button className="btn" disabled={!prev} onClick={() => go(prev)}>
          {t('Previous')}
        </button>
        <button className="btn" disabled={!nextEp && !queueNext()} onClick={goNext}>
          {t('Next')}
        </button>
      </div>
    </div>
  )
}

function queueNext() {
  try {
    const q = JSON.parse(sessionStorage.getItem('cvp-queue') || '[]')
    const path = window.location.pathname
    const m = path.match(/^\/watch\/([^/]+)\/([^/]+)\//)
    if (!m || !Array.isArray(q)) return null
    const id = decodeURIComponent(m[2])
    const i = q.findIndex((it) => it.id === id)
    return i >= 0 ? q[i + 1] || null : null
  } catch {
    return null
  }
}
