import { useQuery } from '@tanstack/react-query'
import { apiFetch } from '../lib/api'
import type { AutomationFeedResponse } from '../types/api'

export function useAutomationFeed(limit = 20) {
  return useQuery<AutomationFeedResponse>({
    queryKey: ['dashboard', 'automation-feed'],
    queryFn: () => apiFetch<AutomationFeedResponse>(`/api/dashboard/automation-feed?limit=${limit}`),
    staleTime: 0,
    refetchInterval: 30_000,
    refetchOnWindowFocus: true,
  })
}
