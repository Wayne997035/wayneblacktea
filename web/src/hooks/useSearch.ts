import { useQuery } from '@tanstack/react-query'
import { apiFetch } from '../lib/api'
import type { SearchResponse, SearchResult } from '../types/api'

const MAX_QUERY_LENGTH = 500
const VALID_TYPES = new Set<string>(['knowledge', 'decision', 'task'])

function isValidSearchResult(r: unknown): r is SearchResult {
  if (typeof r !== 'object' || r === null) return false
  const obj = r as Record<string, unknown>
  return (
    VALID_TYPES.has(obj.type as string) &&
    typeof obj.id === 'string' &&
    typeof obj.title === 'string' &&
    typeof obj.content === 'string'
  )
}

export function useSearch(query: string) {
  const trimmed = query.trim().slice(0, MAX_QUERY_LENGTH)

  return useQuery<SearchResponse>({
    queryKey: ['search', trimmed],
    queryFn: async () => {
      const params = new URLSearchParams({ q: trimmed })
      const raw = await apiFetch<SearchResponse>(`/api/search?${params.toString()}`)
      return { ...raw, results: raw.results.filter(isValidSearchResult) }
    },
    enabled: trimmed.length >= 1,
    staleTime: 30_000,
  })
}
