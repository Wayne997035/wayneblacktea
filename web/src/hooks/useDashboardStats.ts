import { useQuery } from '@tanstack/react-query'
import { apiFetch } from '../lib/api'
import type { DashboardStats } from '../types/api'

export function useDashboardStats(period: 7 | 30 = 7) {
  return useQuery<DashboardStats>({
    queryKey: ['dashboard', 'stats', period],
    queryFn: () => apiFetch<DashboardStats>(`/api/dashboard/stats?period=${period}`),
    staleTime: 60_000,
  })
}
