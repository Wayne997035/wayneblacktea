import { useQuery } from '@tanstack/react-query'
import { apiFetch } from '../lib/api'
import type { DueReview } from '../types/api'

export function useDueReviewsDashboard() {
  return useQuery<DueReview[]>({
    queryKey: ['dashboard', 'due-reviews'],
    queryFn: () => apiFetch<DueReview[]>('/api/learning/reviews?limit=50'),
    staleTime: 120_000,
    // Defence: backend may return JSON null for an empty list; never let a null reach .length.
    select: (data) => data ?? [],
  })
}
