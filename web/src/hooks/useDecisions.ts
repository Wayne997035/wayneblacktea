import { useQuery, useMutation, useQueryClient, type QueryClient } from '@tanstack/react-query'
import { apiFetch } from '../lib/api'
import type { Decision, DecisionsPageResponse } from '../types/api'

/**
 * [F1003-15] Any mutation that creates a decision server-side (logging one
 * directly, or accepting a decision-type proposal) must refresh both decision
 * list caches: the legacy `['decisions']` key (TaskRow, ProjectDetailPage)
 * and the paginated `['decisions-feed', ...]` key (DecisionsPage). TanStack
 * Query v5 invalidation matches by key prefix, so invalidating `['decisions']`
 * alone never reaches `['decisions-feed', ...]` — callers MUST invalidate
 * both explicitly.
 */
export function invalidateDecisionLists(queryClient: QueryClient): void {
  void queryClient.invalidateQueries({ queryKey: ['decisions'] })
  void queryClient.invalidateQueries({ queryKey: ['decisions-feed'] })
}

export function useDecisions(projectId?: string, options?: { enabled?: boolean }) {
  return useQuery<Decision[]>({
    queryKey: ['decisions', projectId ?? 'all'],
    queryFn: async () => {
      const url = projectId
        ? `/api/decisions?${new URLSearchParams({ project_id: projectId }).toString()}`
        : '/api/decisions'
      // [F1003-15] /api/decisions now returns {decisions, offset, limit,
      // has_more} on all three filter branches (none / repo_name /
      // project_id). Unwrap here so this hook's existing callers (TaskRow,
      // ProjectDetailPage) keep getting a plain Decision[] untouched.
      const r = await apiFetch<DecisionsPageResponse | null>(url)
      return r?.decisions ?? []
    },
    enabled: options?.enabled ?? true,
    // Defence: backend may return JSON null for an empty list; never let a null reach .length.
    select: (data) => data ?? [],
  })
}

export interface LogDecisionRequest {
  title: string;
  context: string;
  decision: string;
  rationale: string;
  repo_name?: string;
  project_id?: string | null;
  alternatives?: string;
}

export function useLogDecision() {
  const queryClient = useQueryClient()
  return useMutation<Decision, Error, LogDecisionRequest>({
    mutationFn: (data) =>
      apiFetch<Decision>('/api/decisions', {
        method: 'POST',
        body: JSON.stringify(data),
      }),
    onSuccess: () => {
      invalidateDecisionLists(queryClient)
    },
  })
}
