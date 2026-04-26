import { useQuery } from '@tanstack/react-query'
import { apiFetch } from '../lib/api'
import type { Decision } from '../types/api'

export function useDecisions() {
  return useQuery<Decision[]>({
    queryKey: ['decisions'],
    queryFn: () => apiFetch<Decision[]>('/api/decisions'),
  })
}
