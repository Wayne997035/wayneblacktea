/**
 * [F1003-13] [F1003-14] useDecisionsFeed covers: first-page load, fetchNextPage
 * success (page 2 appended), fetchNextPage failure (first page retained,
 * isFetchNextPageError), has_more:false stopping hasNextPage, project/repo
 * filter forwarded as query params on every page, and a filter change
 * discarding stale pages to start a fresh query at offset=0.
 *
 * Pattern follows web/src/hooks/emptyListDefence.test.ts: renderHook +
 * QueryClientProvider wrapper + mocked apiFetch only (never the whole module).
 */
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { renderHook, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import React from 'react'

import { useDecisionsFeed } from './useDecisionsFeed'
import type { Decision, DecisionsPageResponse } from '../types/api'

const apiFetchMock = vi.fn()
vi.mock('../lib/api', () => ({
  apiFetch: (...args: unknown[]) => apiFetchMock(...args),
}))

function makeWrapper() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  const Wrapper = ({ children }: { children: React.ReactNode }) =>
    React.createElement(QueryClientProvider, { client: queryClient }, children)
  return Wrapper
}

function makeDecision(id: string, repoName: string | null = null): Decision {
  return {
    id,
    project_id: null,
    repo_name: repoName,
    title: `decision ${id}`,
    context: 'ctx',
    decision: 'did it',
    rationale: 'why',
    created_at: '2026-01-01T00:00:00Z',
  }
}

function page(ids: string[], offset: number, hasMore: boolean, repoName: string | null = null): DecisionsPageResponse {
  return { decisions: ids.map((id) => makeDecision(id, repoName)), offset, limit: 20, has_more: hasMore }
}

/** Routes apiFetchMock by exact request URL so assertions never depend on call order. */
function routeApiFetch(table: Record<string, DecisionsPageResponse | Error>) {
  apiFetchMock.mockImplementation((url: string) => {
    const entry = table[url]
    if (entry === undefined) return Promise.reject(new Error(`unexpected request: ${url}`))
    if (entry instanceof Error) return Promise.reject(entry)
    return Promise.resolve(entry)
  })
}

describe('useDecisionsFeed', () => {
  beforeEach(() => {
    apiFetchMock.mockReset()
  })

  it('[F1003-13] fetchNextPage appends page 2 (offset=20) below page 1', async () => {
    routeApiFetch({
      '/api/decisions?limit=20&offset=0': page(['d1'], 0, true),
      '/api/decisions?limit=20&offset=20': page(['d2'], 20, false),
    })
    const { result } = renderHook(() => useDecisionsFeed(), { wrapper: makeWrapper() })

    await waitFor(() => expect(result.current.isLoading).toBe(false))
    expect(result.current.hasNextPage).toBe(true)

    void result.current.fetchNextPage()
    await waitFor(() => expect(result.current.data?.pages).toHaveLength(2))

    expect(result.current.data?.pages.flatMap((p) => p.decisions).map((d) => d.id)).toEqual(['d1', 'd2'])
    expect(result.current.hasNextPage).toBe(false)
  })

  it('[F1003-13] fetchNextPage failure keeps page 1 visible and flips isFetchNextPageError', async () => {
    routeApiFetch({
      '/api/decisions?limit=20&offset=0': page(['d1'], 0, true),
      '/api/decisions?limit=20&offset=20': new Error('500'),
    })
    const { result } = renderHook(() => useDecisionsFeed(), { wrapper: makeWrapper() })

    await waitFor(() => expect(result.current.isLoading).toBe(false))

    void result.current.fetchNextPage().catch(() => undefined)
    await waitFor(() => expect(result.current.isFetchNextPageError).toBe(true))
    expect(result.current.data?.pages.map((p) => p.decisions[0].id)).toEqual(['d1'])
    // Must NOT be treated as exhausted — page 1's has_more:true still stands.
    expect(result.current.hasNextPage).toBe(true)
  })

  it('[F1003-13] has_more:false stops hasNextPage and fires no second request', async () => {
    routeApiFetch({ '/api/decisions?limit=20&offset=0': page(['d1'], 0, false) })
    const { result } = renderHook(() => useDecisionsFeed(), { wrapper: makeWrapper() })

    await waitFor(() => expect(result.current.isLoading).toBe(false))
    expect(result.current.hasNextPage).toBe(false)
    expect(apiFetchMock).toHaveBeenCalledTimes(1)
  })

  it('[F1003-14] forwards repoName as repo_name on every page request', async () => {
    routeApiFetch({
      '/api/decisions?limit=20&offset=0&repo_name=wayneblacktea': page(['d1'], 0, true, 'wayneblacktea'),
      '/api/decisions?limit=20&offset=20&repo_name=wayneblacktea': page(['d2'], 20, false, 'wayneblacktea'),
    })
    const { result } = renderHook(() => useDecisionsFeed({ repoName: 'wayneblacktea' }), { wrapper: makeWrapper() })

    await waitFor(() => expect(result.current.isLoading).toBe(false))
    void result.current.fetchNextPage()
    await waitFor(() => expect(result.current.data?.pages).toHaveLength(2))
  })

  it('[F1003-14] forwards projectId as project_id', async () => {
    routeApiFetch({ '/api/decisions?limit=20&offset=0&project_id=p1': page(['d1'], 0, false) })
    const { result } = renderHook(() => useDecisionsFeed({ projectId: 'p1' }), { wrapper: makeWrapper() })

    await waitFor(() => expect(result.current.isLoading).toBe(false))
    expect(result.current.data?.pages[0].decisions[0].id).toBe('d1')
  })

  it('[F1003-14] filter change discards stale pages: data.pages.length===1, all rows match the new repo_name', async () => {
    routeApiFetch({
      '/api/decisions?limit=20&offset=0': page(['d1'], 0, true),
      '/api/decisions?limit=20&offset=20': page(['d2'], 20, false),
      '/api/decisions?limit=20&offset=0&repo_name=wayneblacktea': page(['d3'], 0, false, 'wayneblacktea'),
    })

    const { result, rerender } = renderHook(
      ({ repoName }: { repoName?: string }) => useDecisionsFeed({ repoName }),
      { wrapper: makeWrapper(), initialProps: { repoName: undefined as string | undefined } },
    )

    await waitFor(() => expect(result.current.isLoading).toBe(false))
    void result.current.fetchNextPage()
    await waitFor(() => expect(result.current.data?.pages).toHaveLength(2))

    rerender({ repoName: 'wayneblacktea' })

    await waitFor(() => expect(result.current.data?.pages.length).toBe(1))
    expect(result.current.data?.pages[0].decisions.every((d) => d.repo_name === 'wayneblacktea')).toBe(true)
  })
})
