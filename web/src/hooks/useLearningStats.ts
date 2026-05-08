import { useQuery } from '@tanstack/react-query'
import { apiFetch } from '../lib/api'
import type { LearningStats } from '../types/api'

export function useLearningStats() {
  return useQuery<LearningStats>({
    queryKey: ['learning', 'stats'],
    queryFn: () => apiFetch<LearningStats>('/api/learning/stats'),
    staleTime: 60_000,
  })
}
