import { useEffect, useState } from 'react'
import { get, post } from '../api.js'
import { setLanguage, t } from '../i18n.js'

export default function SettingsDialog({ onClose, onChanged }) {
  const [data, setData] = useState(null)
  const [folder, setFolder] = useState('')
  const [err, setErr] = useState('')
  const [busy, setBusy] = useState(false)

  const load = () => {
    get('/api/settings')
      .then(setData)
      .catch((e) => setErr(e.message))
  }
  useEffect(load, [])

  const disabled = new Set((data && data.disabledPlugins) || [])

  const setLang = async (code) => {
    setLanguage(code)
    const next = await post('/api/settings', { language: code })
    setData(next)
    onChanged()
  }

  const togglePlugin = async (id, on) => {
    const nextOff = new Set(data.disabledPlugins || [])
    if (on) nextOff.delete(id)
    else nextOff.add(id)
    const next = await post('/api/settings', { disabledPlugins: [...nextOff] })
    setData(next)
    onChanged()
  }

  const changeFolder = async (path, remove) => {
    setErr('')
    setBusy(true)
    try {
      const d = await post('/api/local/folder', { path, remove })
      setData((old) => ({ ...old, localFolders: d.folders || [] }))
      setFolder('')
      onChanged()
    } catch (e) {
      setErr(e.message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="veil" onClick={onClose}>
      <div className="dialog" onClick={(e) => e.stopPropagation()} role="dialog" aria-label={t('Settings')}>
        <div className="dialog-head">
          <h2>{t('Settings')}</h2>
          <button className="icon-btn" onClick={onClose} aria-label={t('Close')}>
            ×
          </button>
        </div>
        {!data ? (
          <div className="hint">{err || t('Loading')}</div>
        ) : (
          <>
            <label className="set-row">
              <span>{t('Language')}</span>
              <select value={data.language || 'en'} onChange={(e) => setLang(e.target.value)}>
                {(data.languages || []).map((l) => (
                  <option key={l.code} value={l.code}>
                    {l.name}
                  </option>
                ))}
              </select>
            </label>

            <div className="set-block">
              <div className="set-label">{t('Plugins')}</div>
              {(data.plugins || []).map((p) => (
                <label className="set-check" key={p.id}>
                  <input
                    type="checkbox"
                    checked={!disabled.has(p.id)}
                    onChange={(e) => togglePlugin(p.id, e.target.checked)}
                  />
                  <span>
                    <strong>{p.name}</strong>
                    <em>{p.id}</em>
                  </span>
                </label>
              ))}
            </div>

            <div className="set-block">
              <div className="set-label">{t('Local')}</div>
              <ul className="folders">
                {(data.localFolders || []).map((p) => (
                  <li key={p}>
                    <span>{p}</span>
                    <button type="button" onClick={() => changeFolder(p, true)}>
                      {t('Remove')}
                    </button>
                  </li>
                ))}
              </ul>
              <form
                className="folder-add"
                onSubmit={(e) => {
                  e.preventDefault()
                  if (folder.trim()) changeFolder(folder.trim(), false)
                }}
              >
                <input
                  className="field"
                  placeholder={t('Folder path on the server')}
                  value={folder}
                  onChange={(e) => setFolder(e.target.value)}
                />
                <button className="btn" disabled={busy || !folder.trim()}>
                  {t('Add')}
                </button>
              </form>
              {err && <div className="login-err">{err}</div>}
            </div>
          </>
        )}
      </div>
    </div>
  )
}
