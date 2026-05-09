import { useAuthStore } from '../stores/authStore'

const BASE = import.meta.env.VITE_API_BASE_URL ?? ''

export async function loginWithKey(key: string): Promise<void> {
  const res = await fetch(`${BASE}/api/session`, {
    method: 'POST',
    credentials: 'include',
    headers: { 'X-API-Key': key },
  })
  if (res.status === 401) {
    throw new Error('401')
  }
  if (res.status === 429) {
    throw new Error('429')
  }
  if (!res.ok) {
    throw new Error(`${res.status}`)
  }
}

export async function apiFetch<T>(path: string, init?: RequestInit): Promise<T> {
  const opts: RequestInit = {
    ...init,
    credentials: 'include',
    headers: { 'Content-Type': 'application/json', ...init?.headers },
  }

  const res = await fetch(`${BASE}${path}`, opts)

  if (res.status === 401) {
    useAuthStore.getState().setStatus('unauthenticated')
    throw new Error('401: session expired')
  }

  if (!res.ok) throw new Error(`${res.status}: ${await res.text()}`)
  return res.json() as Promise<T>
}
