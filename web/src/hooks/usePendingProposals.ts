import { useQuery } from '@tanstack/react-query'
import { apiFetch } from '../lib/api'
import type { PendingProposal } from '../types/api'

/** Fetches all pending proposals across all types (concept, knowledge, goal, project, task). */
export function usePendingProposals() {
  return useQuery<PendingProposal[]>({
    queryKey: ['proposals', 'pending'],
    queryFn: () => apiFetch<PendingProposal[]>('/api/proposals/pending'),
    staleTime: 60_000,
    // Defence: backend may return JSON null for an empty list; never let a null reach .length.
    select: (data) => data ?? [],
  })
}

/**
 * Hook for fetching proposals by status (pending|accepted|rejected|all).
 * Returns all proposal types; components filter/group by type as needed.
 */
export function useAllProposals(status: string) {
  return useQuery<PendingProposal[]>({
    queryKey: ['proposals', 'all', status],
    queryFn: async () => {
      if (status === 'pending') {
        return apiFetch<PendingProposal[]>('/api/proposals/pending')
      }
      try {
        const params = new URLSearchParams({ status })
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
    // Defence: backend may return JSON null for an empty list; never let a null reach .length.
    select: (data) => data ?? [],
  })
}
