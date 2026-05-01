import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { apiFetch } from '../lib/api'
import type { Decision } from '../types/api'

export function useDecisions(projectId?: string) {
  return useQuery<Decision[]>({
    queryKey: ['decisions', projectId ?? 'all'],
    queryFn: () => {
      const url = projectId
        ? `/api/decisions?${new URLSearchParams({ project_id: projectId }).toString()}`
        : '/api/decisions'
      return apiFetch<Decision[]>(url)
    },
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
      void queryClient.invalidateQueries({ queryKey: ['decisions'] })
    },
  })
}
