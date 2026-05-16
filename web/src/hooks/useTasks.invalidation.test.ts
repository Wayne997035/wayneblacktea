/**
 * Tests that useCompleteTask and useCreateTask invalidate the new
 * dashboard query keys after a successful mutation.
 */
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { renderHook, act, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import React from 'react'
import { useCompleteTask, useCreateTask } from './useTasks'

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

describe('useCompleteTask invalidations', () => {
  beforeEach(() => apiFetchMock.mockReset())

  it('invalidates dashboard/stats and dashboard/automation-feed on success', async () => {
    const { Wrapper, queryClient } = makeWrapper()
    const invalidateSpy = vi.spyOn(queryClient, 'invalidateQueries')

    apiFetchMock.mockResolvedValueOnce({ id: 'task-1', status: 'completed' })

    const { result } = renderHook(() => useCompleteTask(), { wrapper: Wrapper })

    await act(async () => {
      result.current.mutate('task-1')
    })

    await waitFor(() => expect(result.current.isSuccess).toBe(true))

    const calledKeys = invalidateSpy.mock.calls.map((c) => JSON.stringify((c[0] as { queryKey: unknown[] }).queryKey))
    expect(calledKeys).toContain(JSON.stringify(['dashboard', 'stats']))
    expect(calledKeys).toContain(JSON.stringify(['dashboard', 'automation-feed']))
  })
})

describe('useCreateTask invalidations', () => {
  beforeEach(() => apiFetchMock.mockReset())

  it('invalidates dashboard/stats and dashboard/automation-feed on success', async () => {
    const { Wrapper, queryClient } = makeWrapper()
    const invalidateSpy = vi.spyOn(queryClient, 'invalidateQueries')

    apiFetchMock.mockResolvedValueOnce({ id: 'new-task', status: 'pending' })

    const { result } = renderHook(() => useCreateTask(), { wrapper: Wrapper })

    await act(async () => {
      result.current.mutate({ title: 'Test task', priority: 2 })
    })

    await waitFor(() => expect(result.current.isSuccess).toBe(true))

    const calledKeys = invalidateSpy.mock.calls.map((c) => JSON.stringify((c[0] as { queryKey: unknown[] }).queryKey))
    expect(calledKeys).toContain(JSON.stringify(['dashboard', 'stats']))
    expect(calledKeys).toContain(JSON.stringify(['dashboard', 'automation-feed']))
  })
})
