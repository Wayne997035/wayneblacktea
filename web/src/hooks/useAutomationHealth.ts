import { useQuery } from '@tanstack/react-query'
import { apiFetch } from '../lib/api'
import type { AutomationHealth } from '../types/api'

export function useAutomationHealth() {
  return useQuery<AutomationHealth>({
    queryKey: ['dashboard', 'automation-health'],
    queryFn: () => apiFetch<AutomationHealth>('/api/dashboard/automation-health'),
    staleTime: 0,
    refetchInterval: 60_000,
    refetchOnWindowFocus: true,
  })
}
