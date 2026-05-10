import { useQuery } from '@tanstack/react-query'
import { apiFetch } from '../lib/api'
import type { RepoOverview } from '../types/api'

/**
 * useRepoOverview fetches GET /api/workspace/repos/:id/overview, the
 * drill-down payload powering /workspace/repos/:id. Disabled until repoId is
 * a non-empty string so we never hit the API with an undefined param.
 */
export function useRepoOverview(repoId: string) {
  return useQuery<RepoOverview>({
    queryKey: ['workspace', 'repos', repoId, 'overview'],
    queryFn: () => apiFetch<RepoOverview>(`/api/workspace/repos/${repoId}/overview`),
    enabled: Boolean(repoId),
  })
}
