import { useQuery } from '@tanstack/react-query'
import { apiFetch } from '../lib/api'
import type { ConceptHistoryItem } from '../types/api'

export function useLearningHistory(status?: string) {
  return useQuery<ConceptHistoryItem[]>({
    queryKey: ['learning', 'history', status ?? 'all'],
    queryFn: () => {
      const params = status && status !== 'all' ? `?status=${status}` : ''
      return apiFetch<ConceptHistoryItem[]>(`/api/learning/history${params}`)
    },
    staleTime: 30_000,
    // Defence: backend may return JSON null for an empty list; never let a null reach .length.
    select: (data) => data ?? [],
  })
}
