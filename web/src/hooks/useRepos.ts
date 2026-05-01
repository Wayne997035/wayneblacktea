import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { apiFetch } from '../lib/api'
import type { Repo } from '../types/api'

export function useRepos() {
  return useQuery<Repo[]>({
    queryKey: ['workspace', 'repos'],
    queryFn: () => apiFetch<Repo[]>('/api/workspace/repos'),
  })
}

export interface UpsertRepoRequest {
  name: string;
  path?: string;
  description?: string;
  language?: string;
  current_branch?: string;
  known_issues?: string[];
  next_planned_step?: string;
}

export function useUpsertRepo() {
  const queryClient = useQueryClient()
  return useMutation<Repo, Error, UpsertRepoRequest>({
    mutationFn: (data) =>
      apiFetch<Repo>('/api/workspace/repos', {
        method: 'POST',
        body: JSON.stringify(data),
      }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['workspace', 'repos'] })
    },
  })
}

export function useRefreshRepos() {
  const queryClient = useQueryClient()
  return () => queryClient.invalidateQueries({ queryKey: ['workspace', 'repos'] })
}
