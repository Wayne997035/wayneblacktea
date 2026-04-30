const BASE = import.meta.env.VITE_API_BASE_URL ?? ''

// Singleton: at most one in-flight initSession at a time.
let _sessionPromise: Promise<void> | null = null

export function initSession(): Promise<void> {
  if (!_sessionPromise) {
    const apiKey = import.meta.env.VITE_API_KEY as string | undefined
    _sessionPromise = fetch(`${BASE}/api/session`, {
      method: 'POST',
      credentials: 'include',
      headers: { 'X-API-Key': apiKey ?? '' },
    })
      .then(() => undefined)
      .catch(() => undefined)
      .finally(() => {
        // Allow re-issue after the session expires (next 401 will trigger again).
        _sessionPromise = null
      })
  }
  return _sessionPromise
}

export async function apiFetch<T>(path: string, init?: RequestInit): Promise<T> {
  const opts: RequestInit = {
    ...init,
    credentials: 'include',
    headers: { 'Content-Type': 'application/json', ...init?.headers },
  }

  let res = await fetch(`${BASE}${path}`, opts)

  if (res.status === 401) {
    // Cookie expired or not yet set — refresh session and retry once.
    await initSession()
    res = await fetch(`${BASE}${path}`, opts)
  }

  if (!res.ok) throw new Error(`${res.status}: ${await res.text()}`)
  return res.json() as Promise<T>
}
