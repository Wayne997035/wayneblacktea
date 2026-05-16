import { describe, it, expect, vi, beforeEach } from 'vitest'
import { renderHook, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import React from 'react'
import { useDashboardUpcoming } from './useDashboardUpcoming'

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

describe('useDashboardUpcoming', () => {
  beforeEach(() => {
    apiFetchMock.mockReset()
  })

  it('is importable and exports useDashboardUpcoming function', () => {
    expect(typeof useDashboardUpcoming).toBe('function')
  })

  it('calls /api/dashboard/upcoming and returns data', async () => {
    const tomorrow = new Date()
    tomorrow.setDate(tomorrow.getDate() + 1)
    const tasks = [
      { id: 'task-1', title: 'Test task', due_date: tomorrow.toISOString(), status: 'pending', priority: 1 },
    ]
    apiFetchMock.mockResolvedValueOnce(tasks)

    const { result } = renderHook(() => useDashboardUpcoming(), { wrapper: makeWrapper() })

    await waitFor(() => expect(result.current.isLoading).toBe(false))
    expect(apiFetchMock).toHaveBeenCalledWith('/api/dashboard/upcoming')
    expect(result.current.data).toEqual(tasks)
  })

  it('returns isLoading true while fetching', () => {
    apiFetchMock.mockReturnValueOnce(new Promise(() => {}))

    const { result } = renderHook(() => useDashboardUpcoming(), { wrapper: makeWrapper() })

    expect(result.current.isLoading).toBe(true)
  })
})
