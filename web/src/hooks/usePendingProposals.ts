import { useQuery } from '@tanstack/react-query'
import { apiFetch } from '../lib/api'
import type { PendingProposal } from '../types/api'

export function usePendingProposals() {
  return useQuery<PendingProposal[]>({
    queryKey: ['proposals', 'pending'],
    queryFn: () => {
      const params = new URLSearchParams({ type: 'concept' })
      return apiFetch<PendingProposal[]>(`/api/proposals/pending?${params.toString()}`)
    },
    staleTime: 60_000,
  })
}
