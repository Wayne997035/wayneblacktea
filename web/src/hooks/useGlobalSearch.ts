import { useQuery } from '@tanstack/react-query'
import { apiFetch } from '../lib/api'
import type { SearchResponse, SearchResult } from '../types/api'

const MAX_QUERY_LENGTH = 500
const VALID_TYPES = new Set<string>(['knowledge', 'decision', 'task', 'project'])

/**
 * Normalises a raw backend result into the canonical SearchResult shape.
 * The backend may return 'content' instead of 'snippet' on older versions.
 */
function normalise(r: unknown): SearchResult | null {
  if (typeof r !== 'object' || r === null) return null
  const obj = r as Record<string, unknown>
  const type = obj.type as string
  if (!VALID_TYPES.has(type)) return null
  if (typeof obj.id !== 'string') return null
  if (typeof obj.title !== 'string') return null

  const snippet = typeof obj.snippet === 'string'
    ? obj.snippet
    : typeof obj.content === 'string'
      ? obj.content
      : ''

  // url MUST be a relative path — reject absolute URLs to prevent open
  // redirect. `startsWith('/')` alone passes protocol-relative `//evil.com`
  // and Windows-style `/\evil.com`, both of which React Router treats as
  // external navigations. Require exactly one leading `/` followed by a
  // non-slash, non-backslash character (or be the bare root `/`).
  const rawUrl = typeof obj.url === 'string' ? obj.url : null
  const url = rawUrl && isSafeRelativeUrl(rawUrl)
    ? rawUrl
    : FALLBACK_URL[type as SearchResult['type']]

  return {
    type: type as SearchResult['type'],
    id: obj.id,
    title: obj.title,
    snippet,
    url,
    score: typeof obj.score === 'number' ? obj.score : null,
  }
}

const FALLBACK_URL: Record<SearchResult['type'], string> = {
  knowledge: '/knowledge',
  decision: '/decisions',
  task: '/gtd',
  project: '/workspace',
}

// isSafeRelativeUrl returns true only for in-app paths. It rejects
// protocol-relative (`//x`), backslash (`/\x`), absolute (`https://x`,
// `javascript:`, `data:`), and any non-string-path input.
export function isSafeRelativeUrl(u: string): boolean {
  if (u === '/') return true
  return /^\/[^/\\]/.test(u)
}

export function useGlobalSearch(query: string) {
  const trimmed = query.trim().slice(0, MAX_QUERY_LENGTH)

  return useQuery<SearchResponse>({
    queryKey: ['globalSearch', trimmed],
    queryFn: async () => {
      const params = new URLSearchParams({ q: trimmed, limit: '10' })
      const raw = await apiFetch<{ query: string; results: unknown[] }>(
        `/api/search?${params.toString()}`,
      )
      const results = Array.isArray(raw.results)
        ? raw.results.flatMap((r) => {
            const n = normalise(r)
            return n ? [n] : []
          })
        : []
      return { query: raw.query ?? trimmed, results }
    },
    enabled: trimmed.length >= 1,
    staleTime: 30_000,
  })
}
