import { useQuery } from '@tanstack/react-query'
import { apiFetch } from '../lib/api'
import type { PendingProposal } from '../types/api'

/** Original hook — fetches only pending proposals (no regression). */
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

/**
 * Hook for fetching proposals by status (pending|accepted|rejected|all).
 * For "pending", uses the existing /api/proposals/pending endpoint to avoid regression.
 * For other statuses, uses /api/proposals?status=…&type=concept with 404 fallback.
 */
export function useAllProposals(status: string) {
  return useQuery<PendingProposal[]>({
    queryKey: ['proposals', 'all', status],
    queryFn: async () => {
      if (status === 'pending') {
        const params = new URLSearchParams({ type: 'concept' })
        return apiFetch<PendingProposal[]>(`/api/proposals/pending?${params.toString()}`)
      }
      try {
        const params = new URLSearchParams({ status, type: 'concept' })
        return await apiFetch<PendingProposal[]>(`/api/proposals?${params.toString()}`)
      } catch (err: unknown) {
        // Graceful fallback: if endpoint returns 404 (not deployed yet), return empty list.
        if (err instanceof Error && err.message.startsWith('404')) {
          return []
        }
        throw err
      }
    },
    staleTime: 60_000,
  })
}
