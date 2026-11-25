// api is everything the page says to the server. A 401 means the login has
// gone; the shell shows the login itself.
export class Unauthorized extends Error {}

export async function api(path, opts = {}) {
  const res = await fetch(path, {
    credentials: 'same-origin',
    headers: opts.body ? { 'Content-Type': 'application/json' } : {},
    ...opts,
    body: opts.body ? JSON.stringify(opts.body) : undefined,
  })
  if (res.status === 401) {
    throw new Unauthorized()
  }
  const text = await res.text()
  let data = null
  try {
    data = text ? JSON.parse(text) : null
  } catch {
    // http.Error writes a plain body.
  }
  if (!res.ok) {
    throw new Error((data && data.error) || `HTTP ${res.status}`)
  }
  return data
}

export const get = (path) => api(path)
export const post = (path, body) => api(path, { method: 'POST', body })
export const del = (path) => api(path, { method: 'DELETE' })

export const coverUrl = (u) => (u ? `/api/cover?u=${encodeURIComponent(u)}` : '')

export function isPlaylistTab(id) {
  return (
    id === 'watchlater' ||
    (id || '').startsWith('fav:') ||
    (id || '').startsWith('season:') ||
    (id || '').startsWith('dir:')
  )
}
