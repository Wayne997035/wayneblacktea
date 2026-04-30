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
 * initSession calls POST /api/session once at SPA startup so the server can
 * set the wbt_session httpOnly cookie.  Subsequent apiFetch calls carry the
 * cookie automatically via credentials: 'include'.
 *
 * The endpoint requires the raw API key in X-API-Key.  The SPA reads it from
 * the build-time env var VITE_API_KEY — never exposed to end users because
 * it is only sent to our own origin to obtain a short-lived session cookie.
 */
export async function initSession(): Promise<void> {
  const apiKey = import.meta.env.VITE_API_KEY as string | undefined
  await fetch(`${BASE}/api/session`, {
    method: 'POST',
    credentials: 'include',
    headers: {
      'X-API-Key': apiKey ?? '',
    },
  })
}
