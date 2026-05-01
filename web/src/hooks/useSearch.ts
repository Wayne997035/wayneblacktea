import { useQuery } from '@tanstack/react-query'
import { apiFetch } from '../lib/api'
import type { SearchResponse } from '../types/api'

const MAX_QUERY_LENGTH = 500

export function useSearch(query: string) {
  const trimmed = query.trim().slice(0, MAX_QUERY_LENGTH)

  return useQuery<SearchResponse>({
    queryKey: ['search', trimmed],
    queryFn: () => {
      const params = new URLSearchParams({ q: trimmed })
      return apiFetch<SearchResponse>(`/api/search?${params.toString()}`)
    },
    enabled: trimmed.length >= 1,
    staleTime: 30_000,
  })
}
