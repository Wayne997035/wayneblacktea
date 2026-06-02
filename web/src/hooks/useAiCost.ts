import { useQuery } from '@tanstack/react-query'
import { apiFetch } from '../lib/api'
import type { AiCostResponse } from '../types/api'

export function useAiCost() {
  return useQuery<AiCostResponse>({
    queryKey: ['dashboard', 'ai-cost'],
    queryFn: () => apiFetch<AiCostResponse>('/api/dashboard/ai-cost'),
    staleTime: 4 * 60_000, // 4 min: slightly under the 5 min refetch interval
    refetchInterval: 5 * 60_000,
  })
}
