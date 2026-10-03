/**
 * [F1003-15] GET /api/decisions now returns {decisions, offset, limit,
 * has_more} on all three filter branches (none / repo_name / project_id).
 * useDecisions's queryFn must unwrap that envelope so its existing callers
 * (TaskRow.tsx, ProjectDetailPage.tsx) keep receiving a plain Decision[].
 *
 * Mocks only `apiFetch` (never the whole useDecisions module) so this test
 * actually exercises the unwrap line, not just a canned return value.
 */
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { renderHook, waitFor, act } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import React from 'react'

import { useDecisions, useLogDecision } from './useDecisions'
import type { Decision } from '../types/api'

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

function makeWrapperWithClient() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  const Wrapper = ({ children }: { children: React.ReactNode }) =>
    React.createElement(QueryClientProvider, { client: queryClient }, children)
  return { Wrapper, queryClient }
}

function calledKeysOf(spy: { mock: { calls: unknown[][] } }) {
  return spy.mock.calls.map((c: unknown[]) => JSON.stringify((c[0] as { queryKey: unknown[] }).queryKey))
}

const sampleDecision: Decision = {
  id: 'd1',
  project_id: 'p1',
  repo_name: 'wayneblacktea',
  title: 'Use X',
  context: 'ctx',
  decision: 'did X',
  rationale: 'why',
  created_at: '2026-01-01T00:00:00Z',
}

describe('useDecisions unwraps the paginated object response', () => {
  beforeEach(() => {
    apiFetchMock.mockReset()
  })

  it('[F1003-15] useDecisions(): {decisions,...} unwraps into Decision[]', async () => {
    apiFetchMock.mockResolvedValueOnce({ decisions: [sampleDecision], offset: 0, limit: 20, has_more: false })
    const { result } = renderHook(() => useDecisions(), { wrapper: makeWrapper() })

    await waitFor(() => expect(result.current.isLoading).toBe(false))
    expect(result.current.isError).toBe(false)
    expect(result.current.data).toEqual([sampleDecision])
  })

  it('[F1003-15] useDecisions(projectId): {decisions,...} unwraps into Decision[]', async () => {
    apiFetchMock.mockResolvedValueOnce({ decisions: [sampleDecision], offset: 0, limit: 20, has_more: false })
    const { result } = renderHook(() => useDecisions('p1'), { wrapper: makeWrapper() })

    await waitFor(() => expect(result.current.isLoading).toBe(false))
    expect(result.current.isError).toBe(false)
    expect(result.current.data).toEqual([sampleDecision])
    expect(apiFetchMock).toHaveBeenCalledWith('/api/decisions?project_id=p1')
  })
})

describe('useLogDecision invalidates both decision list caches on success', () => {
  beforeEach(() => apiFetchMock.mockReset())

  it('[F1003-15] useLogDecision(): invalidated keys include both decisions and decisions-feed', async () => {
    const { Wrapper, queryClient } = makeWrapperWithClient()
    const invalidateSpy = vi.spyOn(queryClient, 'invalidateQueries')
    apiFetchMock.mockResolvedValueOnce(sampleDecision)

    const { result } = renderHook(() => useLogDecision(), { wrapper: Wrapper })

    await act(async () => {
      result.current.mutate({
        title: sampleDecision.title,
        context: sampleDecision.context,
        decision: sampleDecision.decision,
        rationale: sampleDecision.rationale,
      })
    })

    await waitFor(() => expect(result.current.isSuccess).toBe(true))

    const calledKeys = calledKeysOf(invalidateSpy)
    expect(calledKeys).toContain(JSON.stringify(['decisions']))
    expect(calledKeys).toContain(JSON.stringify(['decisions-feed']))
  })
})
