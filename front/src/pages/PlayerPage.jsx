import Hls from 'hls.js'
import { useEffect, useRef, useState } from 'react'
import { get, post, del } from '../api.js'
import { t } from '../i18n.js'
import { navigate } from '../App.jsx'

// 播放页：后端把流做成 HLS，这里用 hls.js 播。倍速、自动连播、进度上报都在这。
const rates = [2, 1.5, 1.25, 1, 0.75, 0.5]

export default function PlayerPage({ src, id, ep }) {
  const [detail, setDetail] = useState(null)
  const [session, setSession] = useState(null) // {id, state, error}
  const [rate, setRate] = useState(1)
  const [nextLeft, setNextLeft] = useState(0)
  const [fatal, setFatal] = useState('')
  const videoRef = useRef(null)
  const progressTimer = useRef(null)
  const nextTimer = useRef(null)
  const startedAt = useRef(false)

  const i = detail ? detail.video.episodes.findIndex((e) => e.id === ep) : -1
  const eps = detail?.video?.episodes || []
  const prev = i > 0 ? eps[i - 1] : null
  const next = i >= 0 && i + 1 < eps.length ? eps[i + 1] : null

  useEffect(() => {
    get(`/api/video/${src}/${encodeURIComponent(id)}`).then(setDetail)
  }, [src, id])

  // 发起播放会话；换集就换会话。
  useEffect(() => {
    startedAt.current = false
    setFatal('')
    let live = true
    let sess = null
    post('/api/play', { source: src, item: id, ep })
      .then((d) => {
        if (!live) return
        sess = d
        setSession(d)
      })
      .catch((e) => live && setFatal(e.message))
    return () => {
      live = false
      if (sess) del(`/api/session/${sess.id}`).catch(() => {})
      setSession(null)
    }
  }, [src, id, ep])

  // hls.js 接到 <video> 上；会话没就绪就轮询。
  useEffect(() => {
    if (!session) return
    const video = videoRef.current
    if (!video) return
    let hls = null
    let poll = null

    const attach = () => {
      const url = `/api/session/${session.id}/index.m3u8`
      if (video.canPlayType('application/vnd.apple.mpegurl')) {
        video.src = url
      } else if (Hls.isSupported()) {
        hls = new Hls({ liveDurationInfinity: true, backBufferLength: 90 })
        hls.loadSource(url)
        hls.attachMedia(video)
        hls.on(Hls.Events.ERROR, (_, data) => {
          if (data.fatal && data.type === Hls.ErrorTypes.NETWORK_ERROR && !poll) {
            // 会话可能还在排队：playlist 404，等一会再试。
            poll = setTimeout(attach, 1500)
          } else if (data.fatal) {
            setFatal('播放出错，稍后重试')
          }
        })
      } else {
        setFatal('这个浏览器播不了这个流')
      }
    }

    const wait = setInterval(async () => {
      const st = await get(`/api/session/${session.id}`).catch(() => null)
      if (!st) return
      setSession((old) => (old ? { ...old, state: st.state, error: st.error } : old))
      if (st.state === 'ready' && !video.dataset.attached) {
        video.dataset.attached = '1'
        attach()
        clearInterval(wait)
      }
      if (st.state === 'error') {
        clearInterval(wait)
        setFatal(st.error || t('error'))
      }
    }, 700)

    return () => {
      clearInterval(wait)
      if (poll) clearTimeout(poll)
      if (hls) hls.destroy()
      delete video.dataset.attached
    }
  }, [session?.id])

  // 续播：metadata 一到，把进度 seek 过去。
  useEffect(() => {
    const video = videoRef.current
    if (!video || !detail?.progress) return
    const p = detail.progress
    if (p.epId !== ep) return
    const onMeta = () => {
      if (!startedAt.current && p.pos > 10 && !p.done) {
        video.currentTime = p.pos - 2
        startedAt.current = true
      }
    }
    video.addEventListener('loadedmetadata', onMeta)
    return () => video.removeEventListener('loadedmetadata', onMeta)
  }, [detail, ep, session?.state])

  // 进度每 10 秒报一次，播完报 done。
  useEffect(() => {
    if (!session) return
    const video = videoRef.current
    if (!video) return
    progressTimer.current = setInterval(() => {
      if (video.currentTime > 0 && !video.paused) {
        post(`/api/session/${session.id}/progress`, {
          pos: video.currentTime,
          dur: video.duration || 0,
          done: false,
        }).catch(() => {})
      }
    }, 10000)
    const onEnded = () => {
      post(`/api/session/${session.id}/progress`, {
        pos: video.duration || 0,
        dur: video.duration || 0,
        done: true,
      }).catch(() => {})
      if (next) {
        setNextLeft(8)
      }
    }
    video.addEventListener('ended', onEnded)
    return () => {
      clearInterval(progressTimer.current)
      video.removeEventListener('ended', onEnded)
    }
  }, [session?.id, next])

  // 连播倒计时。
  useEffect(() => {
    if (nextLeft <= 0) return
    nextTimer.current = setTimeout(() => {
      if (nextLeft <= 1) {
        setNextLeft(0)
        go(next)
      } else {
        setNextLeft(nextLeft - 1)
      }
    }, 1000)
    return () => clearTimeout(nextTimer.current)
  }, [nextLeft])

  const go = (e) => {
    if (!e) return
    setNextLeft(0)
    navigate(`/watch/${src}/${encodeURIComponent(id)}/${encodeURIComponent(e.ID)}`)
  }

  const changeRate = (r) => {
    setRate(r)
    if (videoRef.current) videoRef.current.playbackRate = r
  }

  return (
    <div className="player-page">
      <div className="player-top">
        <button className="chip" onClick={() => navigate(`/detail/${src}/${encodeURIComponent(id)}`)}>
          ← {detail?.video?.Title || t('back')}
        </button>
        <span className="player-ep">
          {eps[i]?.title || t('ep', i + 1)}
        </span>
        <div className="player-rate">
          {rates.map((r) => (
            <button key={r} className={'chip' + (r === rate ? ' chip-on' : '')} onClick={() => changeRate(r)}>
              {r}×
            </button>
          ))}
        </div>
      </div>

      <div className="stage">
        <video ref={videoRef} controls autoPlay playsInline />
        {!session && <div className="stage-overlay">{t('starting')}</div>}
        {session && (session.state === 'queued' || session.state === 'making') && !fatal && (
          <div className="stage-overlay">{t('transcoding')}</div>
        )}
        {fatal && (
          <div className="stage-overlay">
            <div>{fatal}</div>
            <button className="btn" onClick={() => window.location.reload()}>
              {t('retry')}
            </button>
          </div>
        )}
        {nextLeft > 0 && (
          <div className="upnext">
            <span>{t('upNext', nextLeft)}</span>
            <button className="btn btn-accent" onClick={() => go(next)}>
              {t('playNow')}
            </button>
            <button className="btn" onClick={() => setNextLeft(0)}>
              {t('cancel')}
            </button>
          </div>
        )}
      </div>

      <div className="player-controls">
        <button className="btn" disabled={!prev} onClick={() => go(prev)}>
          ⏮ {t('prev')}
        </button>
        <button className="btn" disabled={!next} onClick={() => go(next)}>
          {t('next')} ⏭
        </button>
      </div>
    </div>
  )
}
