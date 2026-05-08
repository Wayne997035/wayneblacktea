import { useQuery } from '@tanstack/react-query'
import { apiFetch } from '../lib/api'
import type { Decision } from '../types/api'

export function useDashboardRecentDecisions() {
  return useQuery<Decision[]>({
    queryKey: ['dashboard', 'recent-decisions'],
    queryFn: () => apiFetch<Decision[]>('/api/dashboard/recent-decisions?limit=3'),
    staleTime: 120_000,
  })
}
