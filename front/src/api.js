// api 是网页对后端的所有话。没登录的回答（401）把页面带去登录。
export class Unauthorized extends Error {}

export async function api(path, opts = {}) {
  const res = await fetch(path, {
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
    // 非 JSON 的错误体（http.Error 直写的）
  }
  if (!res.ok) {
    throw new Error((data && data.error) || `请求失败（${res.status}）`)
  }
  return data
}

export const get = (path) => api(path)
export const post = (path, body) => api(path, { method: 'POST', body })
export const del = (path) => api(path, { method: 'DELETE' })

export const coverUrl = (u) =>
  u ? `/api/cover?u=${encodeURIComponent(u)}` : ''
