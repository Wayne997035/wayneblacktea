const BASE = import.meta.env.VITE_API_BASE_URL ?? ''

export async function apiFetch<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(`${BASE}${path}`, {
    ...init,
    credentials: 'include',
    headers: {
      'Content-Type': 'application/json',
      ...init?.headers,
    },
  })
  if (!res.ok) throw new Error(`${res.status}: ${await res.text()}`)
  return res.json() as Promise<T>
}

/**
 * initSession calls GET /api/session once at SPA startup so the server can
 * set the wbt_session httpOnly cookie.  Subsequent apiFetch calls carry the
 * cookie automatically via credentials: 'include'.
 */
export async function initSession(): Promise<void> {
  await fetch(`${BASE}/api/session`, { credentials: 'include' })
}
