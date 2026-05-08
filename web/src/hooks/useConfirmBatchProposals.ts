import { useMutation, useQueryClient } from '@tanstack/react-query'
import { apiFetch } from '../lib/api'

interface BatchConfirmRequest {
  ids: string[]
  action: 'accept' | 'reject'
}

interface BatchConfirmResponse {
  accepted?: number
  rejected?: number
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
      void qc.invalidateQueries({ queryKey: ['knowledge'] })
    },
  })
}
