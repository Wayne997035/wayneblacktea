import { describe, it, expect, vi, beforeEach } from 'vitest'
import { renderHook, act, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import * as React from 'react'
import { useVision, useCreateVision, useUpdateVision } from './useVision'

// --- Mocks ---

const apiFetchMock = vi.fn()
vi.mock('../lib/api', () => ({
  apiFetch: (...args: unknown[]) => apiFetchMock(...args),
}))

const addToastMock = vi.fn()
vi.mock('../stores/toastStore', () => ({
  useToastStore: (selector: (s: { addToast: typeof addToastMock }) => unknown) =>
    selector({ addToast: addToastMock }),
}))

// --- Helpers ---

function makeWrapper() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  const Wrapper = ({ children }: { children: React.ReactNode }) =>
    React.createElement(QueryClientProvider, { client: queryClient }, children)
  return { Wrapper, queryClient }
}

const fakeItem = {
  id: 'v1',
  title: 'Build AI pipeline',
  why_blocked: 'Needs GPU budget approval',
  depends_on: [],
  status: 'open' as const,
  created_at: '2026-01-01T00:00:00Z',
}

// --- useVision ---

describe('useVision', () => {
  beforeEach(() => apiFetchMock.mockReset())

  it('returns items list on success (happy path)', async () => {
    apiFetchMock.mockResolvedValueOnce([fakeItem])
    const { Wrapper } = makeWrapper()

    const { result } = renderHook(() => useVision(), { wrapper: Wrapper })

    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(result.current.data).toHaveLength(1)
    expect(result.current.data?.[0].title).toBe('Build AI pipeline')
    expect(apiFetchMock).toHaveBeenCalledWith('/api/vision')
  })

  it('passes status filter as query param', async () => {
    apiFetchMock.mockResolvedValueOnce([fakeItem])
    const { Wrapper } = makeWrapper()

    renderHook(() => useVision('discussing'), { wrapper: Wrapper })

    await waitFor(() => expect(apiFetchMock).toHaveBeenCalledWith('/api/vision?status=discussing'))
  })

  it('exposes loading state before fetch resolves', async () => {
    let resolvePromise!: (value: unknown[]) => void
    const pendingPromise = new Promise<unknown[]>((res) => { resolvePromise = res })
    apiFetchMock.mockReturnValueOnce(pendingPromise)
    const { Wrapper } = makeWrapper()

    const { result } = renderHook(() => useVision(), { wrapper: Wrapper })
    expect(result.current.isLoading).toBe(true)

    // Resolve the pending promise so the hook can settle and not leak
    resolvePromise([])
    await waitFor(() => expect(result.current.isLoading).toBe(false))
  })

  it('exposes error state when fetch rejects', async () => {
    apiFetchMock.mockRejectedValueOnce(new Error('500: boom'))
    const { Wrapper } = makeWrapper()

    const { result } = renderHook(() => useVision(), { wrapper: Wrapper })

    await waitFor(() => expect(result.current.isError).toBe(true))
    expect(result.current.error).toBeInstanceOf(Error)
  })
})

// --- useCreateVision ---

describe('useCreateVision', () => {
  beforeEach(() => {
    apiFetchMock.mockReset()
    addToastMock.mockReset()
  })

  it('invalidates [vision] cache on success', async () => {
    apiFetchMock.mockResolvedValueOnce(fakeItem)
    const { Wrapper, queryClient } = makeWrapper()
    const invalidateSpy = vi.spyOn(queryClient, 'invalidateQueries')

    const { result } = renderHook(() => useCreateVision(), { wrapper: Wrapper })

    await act(async () => {
      result.current.mutate({ title: 'Test', why_blocked: 'Budget' })
    })

    await waitFor(() => expect(result.current.isSuccess).toBe(true))

    const calledKeys = invalidateSpy.mock.calls.map(
      (c) => JSON.stringify((c[0] as { queryKey: unknown[] }).queryKey),
    )
    expect(calledKeys).toContain(JSON.stringify(['vision']))
  })

  it('calls addToast with error type on failure', async () => {
    apiFetchMock.mockRejectedValueOnce(new Error('422: why_blocked required'))
    const { Wrapper } = makeWrapper()

    const { result } = renderHook(() => useCreateVision(), { wrapper: Wrapper })

    await act(async () => {
      result.current.mutate({ title: 'Test', why_blocked: '' })
    })

    await waitFor(() => expect(result.current.isError).toBe(true))
    expect(addToastMock).toHaveBeenCalledWith(
      expect.objectContaining({ type: 'error' }),
    )
  })
})

// --- useUpdateVision ---

describe('useUpdateVision', () => {
  beforeEach(() => {
    apiFetchMock.mockReset()
    addToastMock.mockReset()
  })

  it('invalidates [vision] cache on success', async () => {
    apiFetchMock.mockResolvedValueOnce({ ...fakeItem, status: 'discussing' })
    const { Wrapper, queryClient } = makeWrapper()
    const invalidateSpy = vi.spyOn(queryClient, 'invalidateQueries')

    const { result } = renderHook(() => useUpdateVision(), { wrapper: Wrapper })

    await act(async () => {
      result.current.mutate({ id: 'v1', data: { status: 'discussing' } })
    })

    await waitFor(() => expect(result.current.isSuccess).toBe(true))

    const calledKeys = invalidateSpy.mock.calls.map(
      (c) => JSON.stringify((c[0] as { queryKey: unknown[] }).queryKey),
    )
    expect(calledKeys).toContain(JSON.stringify(['vision']))
  })

  it('calls addToast with error type on failure', async () => {
    apiFetchMock.mockRejectedValueOnce(new Error('500: internal server error'))
    const { Wrapper } = makeWrapper()

    const { result } = renderHook(() => useUpdateVision(), { wrapper: Wrapper })

    await act(async () => {
      result.current.mutate({ id: 'v1', data: { status: 'maturing' } })
    })

    await waitFor(() => expect(result.current.isError).toBe(true))
    expect(addToastMock).toHaveBeenCalledWith(
      expect.objectContaining({ type: 'error' }),
    )
  })
})
