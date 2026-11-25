import { useState } from 'react'
import { post } from '../api.js'
import { t } from '../i18n.js'

export default function LoginPage({ onLogin }) {
  const [user, setUser] = useState('')
  const [pass, setPass] = useState('')
  const [err, setErr] = useState('')
  const [busy, setBusy] = useState(false)

  const submit = async (e) => {
    e.preventDefault()
    setErr('')
    setBusy(true)
    try {
      await post('/api/login', { user, pass })
      onLogin()
    } catch {
      setErr(t('Wrong username or password'))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="page-center">
      <form className="login-card" onSubmit={submit}>
        <div className="login-mark">
          <svg viewBox="0 0 24 24" width="22" height="22" aria-hidden="true">
            <rect x="3" y="5" width="18" height="14" rx="2" fill="none" stroke="currentColor" strokeWidth="2" />
            <path d="M10 9.5v5l4.5-2.5z" fill="currentColor" />
          </svg>
        </div>
        <div className="login-title">{t('Codie Video')}</div>
        <input
          className="field"
          placeholder={t('Username')}
          autoComplete="username"
          value={user}
          onChange={(e) => setUser(e.target.value)}
          autoFocus
        />
        <input
          className="field"
          type="password"
          placeholder={t('Password')}
          autoComplete="current-password"
          value={pass}
          onChange={(e) => setPass(e.target.value)}
        />
        {err && <div className="login-err">{err}</div>}
        <button className="btn accent" disabled={busy || !user || !pass}>
          {t('Sign in')}
        </button>
      </form>
    </div>
  )
}
