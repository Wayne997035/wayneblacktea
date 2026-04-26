import { useQuery } from '@tanstack/react-query'
import { apiFetch } from '../lib/api'
import type { Goal } from '../types/api'

export function useGoals() {
  return useQuery<Goal[]>({
    queryKey: ['goals'],
    queryFn: () => apiFetch<Goal[]>('/api/goals'),
  })
}
