import { useInfiniteQuery } from '@tanstack/react-query'
import { apiFetch } from '../lib/api'
import type { DecisionsPageResponse } from '../types/api'

const PAGE_SIZE = 20

export interface UseDecisionsFeedParams {
  projectId?: string
  repoName?: string
}

/**
 * [F1003-13] Paginated decisions feed for DecisionsPage's "load more" control.
 * Replaces the single unfiltered useDecisions() call there; TaskRow and
 * ProjectDetailPage keep using the plain useDecisions() hook untouched.
 *
 * [F1003-14] projectId/repoName are part of the query key, so switching
 * either filter is a brand-new TanStack Query key — the library starts a
 * fresh infinite query at offset 0 under that key and discards the old
 * key's pages automatically (no manual reset needed):
 * https://tanstack.com/query/v5/docs/framework/react/guides/query-keys
 */
export function useDecisionsFeed({ projectId, repoName }: UseDecisionsFeedParams = {}) {
  return useInfiniteQuery<DecisionsPageResponse>({
    queryKey: ['decisions-feed', projectId ?? 'all', repoName ?? 'all'],
    queryFn: ({ pageParam }) => {
      // project_id and repo_name are mutually exclusive per the /api/decisions
      // contract — DecisionsPage's single-select filters never produce both.
      const params = new URLSearchParams({ limit: String(PAGE_SIZE), offset: String(pageParam) })
      if (projectId) params.set('project_id', projectId)
      else if (repoName) params.set('repo_name', repoName)
      return apiFetch<DecisionsPageResponse>(`/api/decisions?${params.toString()}`)
    },
    initialPageParam: 0,
    getNextPageParam: (lastPage) => (lastPage.has_more ? lastPage.offset + lastPage.limit : undefined),
  })
}
