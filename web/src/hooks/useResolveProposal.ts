import { useMutation, useQueryClient } from '@tanstack/react-query'
import { apiFetch } from '../lib/api'
import type { PendingProposal } from '../types/api'
import { invalidateDecisionLists } from './useDecisions'

interface ResolveVars {
  id: string
  action: 'accept' | 'reject'
}

interface ResolveResponse {
  proposal: PendingProposal
  concept?: unknown
}

export function useResolveProposal() {
  const qc = useQueryClient()
  return useMutation<ResolveResponse, Error, ResolveVars>({
    mutationFn: ({ id, action }) =>
      apiFetch<ResolveResponse>(`/api/proposals/${id}/confirm`, {
        method: 'POST',
        body: JSON.stringify({ action }),
      }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ['proposals', 'pending'] })
      void qc.invalidateQueries({ queryKey: ['proposals', 'all'] })
      void qc.invalidateQueries({ queryKey: ['knowledge'] })
      void qc.invalidateQueries({ queryKey: ['dashboard', 'stats'] })
      void qc.invalidateQueries({ queryKey: ['dashboard', 'automation-feed'] })
      // goal/project proposals now create rows server-side (766916a, 198b814);
      // follow the existing useGoals/useProjects mutation convention so the
      // UI reflects the new record instead of waiting for a manual refresh.
      void qc.invalidateQueries({ queryKey: ['goals'] })
      void qc.invalidateQueries({ queryKey: ['projects'] })
      void qc.invalidateQueries({ queryKey: ['context', 'today'] })
      // [F1003-15] accepting a decision-type proposal creates a Decision row
      // server-side — keep both decision list caches in sync too.
      invalidateDecisionLists(qc)
    },
    onError: (error: Error) => {
      // Error state is surfaced via mutation.isError in the calling component.
      // Log here for observability without crashing the UI.
      console.error('useResolveProposal: resolve failed', error)
    },
  })
}
