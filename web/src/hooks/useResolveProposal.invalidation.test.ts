/**
 * Tests that useResolveProposal and useConfirmBatchProposals invalidate the
 * goals/projects/context-today query keys after a successful mutation.
 *
 * Goal/project proposals now create real rows server-side (766916a,
 * 198b814); without these invalidations the UI would stay stale until a
 * manual refresh even though the record was created.
 */
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { renderHook, act, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import React from 'react'
import { useResolveProposal } from './useResolveProposal'
import { useConfirmBatchProposals } from './useConfirmBatchProposals'

const apiFetchMock = vi.fn()
vi.mock('../lib/api', () => ({
  apiFetch: (...args: unknown[]) => apiFetchMock(...args),
}))

function makeWrapper() {
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

describe('useResolveProposal invalidations', () => {
  beforeEach(() => apiFetchMock.mockReset())

  it('invalidates goals, projects, and context/today on success', async () => {
    const { Wrapper, queryClient } = makeWrapper()
    const invalidateSpy = vi.spyOn(queryClient, 'invalidateQueries')

    apiFetchMock.mockResolvedValueOnce({ proposal: { id: 'p-1', type: 'goal' } })

    const { result } = renderHook(() => useResolveProposal(), { wrapper: Wrapper })

    await act(async () => {
      result.current.mutate({ id: 'p-1', action: 'accept' })
    })

    await waitFor(() => expect(result.current.isSuccess).toBe(true))

    const calledKeys = calledKeysOf(invalidateSpy)
    expect(calledKeys).toContain(JSON.stringify(['goals']))
    expect(calledKeys).toContain(JSON.stringify(['projects']))
    expect(calledKeys).toContain(JSON.stringify(['context', 'today']))
    // pre-existing keys must still be invalidated (no regression)
    expect(calledKeys).toContain(JSON.stringify(['proposals', 'pending']))
    expect(calledKeys).toContain(JSON.stringify(['proposals', 'all']))
    expect(calledKeys).toContain(JSON.stringify(['knowledge']))
    expect(calledKeys).toContain(JSON.stringify(['dashboard', 'stats']))
    expect(calledKeys).toContain(JSON.stringify(['dashboard', 'automation-feed']))
  })
})

describe('useConfirmBatchProposals invalidations', () => {
  beforeEach(() => apiFetchMock.mockReset())

  it('invalidates goals, projects, and context/today on success', async () => {
    const { Wrapper, queryClient } = makeWrapper()
    const invalidateSpy = vi.spyOn(queryClient, 'invalidateQueries')

    apiFetchMock.mockResolvedValueOnce({ results: [{ id: 'p-1', ok: true }] })

    const { result } = renderHook(() => useConfirmBatchProposals(), { wrapper: Wrapper })

    await act(async () => {
      result.current.mutate({ ids: ['p-1'], action: 'accept' })
    })

    await waitFor(() => expect(result.current.isSuccess).toBe(true))

    const calledKeys = calledKeysOf(invalidateSpy)
    expect(calledKeys).toContain(JSON.stringify(['goals']))
    expect(calledKeys).toContain(JSON.stringify(['projects']))
    expect(calledKeys).toContain(JSON.stringify(['context', 'today']))
    // pre-existing keys must still be invalidated (no regression)
    expect(calledKeys).toContain(JSON.stringify(['proposals', 'pending']))
    expect(calledKeys).toContain(JSON.stringify(['proposals', 'all']))
    expect(calledKeys).toContain(JSON.stringify(['knowledge']))
    expect(calledKeys).toContain(JSON.stringify(['dashboard', 'stats']))
    expect(calledKeys).toContain(JSON.stringify(['dashboard', 'automation-feed']))
  })
})
