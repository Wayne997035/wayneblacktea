import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { apiFetch } from '../lib/api'
import type { Repo } from '../types/api'

export function useRepos() {
  return useQuery<Repo[]>({
    queryKey: ['workspace', 'repos'],
    queryFn: () => apiFetch<Repo[]>('/api/workspace/repos'),
  })
}

export function useSyncRepos() {
  const queryClient = useQueryClient()
  return useMutation<Repo[], Error, void>({
    mutationFn: () =>
      apiFetch<Repo[]>('/api/workspace/repos', { method: 'POST' }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['workspace', 'repos'] })
    },
  })
}
