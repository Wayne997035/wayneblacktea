import { useQuery } from '@tanstack/react-query'
import { apiFetch } from '../lib/api'
import type { TimelineResponse } from '../types/api'

/**
 * useTimeline fetches aggregated events from GET /api/timeline within
 * [from, to]. Both bounds are RFC3339 UTC. Cache key is keyed on the ISO
 * strings so range changes (month/week navigation) trigger a fresh query
 * but identical ranges share the cached payload.
 */
export function useTimeline(from: Date, to: Date) {
  const fromIso = from.toISOString()
  const toIso = to.toISOString()
  return useQuery<TimelineResponse>({
    queryKey: ['timeline', fromIso, toIso],
    queryFn: () =>
      apiFetch<TimelineResponse>(
        `/api/timeline?${new URLSearchParams({ from: fromIso, to: toIso }).toString()}`,
      ),
    staleTime: 60_000,
  })
}
