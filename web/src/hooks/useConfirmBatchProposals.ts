import { useMutation, useQueryClient } from '@tanstack/react-query'
import { apiFetch } from '../lib/api'

interface BatchConfirmRequest {
  ids: string[]
  action: 'accept' | 'reject'
}

interface BatchConfirmResponse {
  results?: Array<{ id: string; ok: boolean; skipped?: boolean; error?: string }>
}

export function useConfirmBatchProposals() {
  const qc = useQueryClient()
  return useMutation<BatchConfirmResponse, Error, BatchConfirmRequest>({
    mutationFn: (body) =>
      apiFetch<BatchConfirmResponse>('/api/proposals/confirm-batch', {
        method: 'POST',
        body: JSON.stringify(body),
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
    },
  })
}
