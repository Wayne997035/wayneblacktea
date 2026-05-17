import { useMutation, useQueryClient } from '@tanstack/react-query'
import { apiFetch } from '../lib/api'
import type { PendingProposal } from '../types/api'

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
    },
    onError: (error: Error) => {
      // Error state is surfaced via mutation.isError in the calling component.
      // Log here for observability without crashing the UI.
      console.error('useResolveProposal: resolve failed', error)
    },
  })
}
