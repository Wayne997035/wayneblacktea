import { useQuery } from '@tanstack/react-query'
import { apiFetch } from '../lib/api'

export function useApiPing() {
  return useQuery<unknown>({
    queryKey: ['api', 'ping'],
    queryFn: () => apiFetch<unknown>('/health'),
    staleTime: 30_000,
    retry: 1,
  })
}
