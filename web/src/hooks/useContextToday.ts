import { useQuery } from '@tanstack/react-query'
import { apiFetch } from '../lib/api'
import type { TodayContext } from '../types/api'

export function useContextToday() {
  return useQuery<TodayContext>({
    queryKey: ['context', 'today'],
    queryFn: () => apiFetch<TodayContext>('/api/context/today'),
    staleTime: 60_000,
  })
}
