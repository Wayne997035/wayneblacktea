import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { renderHook, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import * as React from 'react'
import { useRepoOverview } from './useRepoOverview'

const apiFetchMock = vi.fn()
vi.mock('../lib/api', () => ({
  apiFetch: (...args: unknown[]) => apiFetchMock(...args),
}))

function wrapperFactory() {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return function Wrapper({ children }: { children: React.ReactNode }) {
    return React.createElement(QueryClientProvider, { client: queryClient }, children)
  }
}

describe('useRepoOverview', () => {
  beforeEach(() => {
    apiFetchMock.mockReset()
  })

  afterEach(() => {
    vi.clearAllMocks()
  })

  it('fetches the overview endpoint with the given repo id', async () => {
    apiFetchMock.mockResolvedValueOnce({
      repo: { id: 'r1', name: 'wbt', status: 'active', known_issues: [] },
      completed_tasks: [],
      pending_tasks: [],
      recent_decisions: [],
      recent_activity: [],
      recent_handoffs: [],
    })

    const { result } = renderHook(() => useRepoOverview('r1'), { wrapper: wrapperFactory() })

    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(apiFetchMock).toHaveBeenCalledWith('/api/workspace/repos/r1/overview')
    expect(result.current.data?.repo.name).toBe('wbt')
  })

  it('does not fire when repoId is empty (enabled=false)', () => {
    renderHook(() => useRepoOverview(''), { wrapper: wrapperFactory() })
    expect(apiFetchMock).not.toHaveBeenCalled()
  })

  it('exposes error state when the fetch rejects', async () => {
    apiFetchMock.mockRejectedValueOnce(new Error('500: boom'))
    const { result } = renderHook(() => useRepoOverview('r1'), { wrapper: wrapperFactory() })
    await waitFor(() => expect(result.current.isError).toBe(true))
    expect(result.current.error).toBeInstanceOf(Error)
  })
})
