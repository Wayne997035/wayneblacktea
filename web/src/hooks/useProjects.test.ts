/**
 * Mutation-testable proof for the `status` param added to useProjects().
 *
 * Root cause this guards against: GtdPage's goal-progress rollup needs every
 * project (including completed/archived ones) to map tasks back to their
 * goal. Before this param existed, GtdPage had no way to ask for anything
 * but the backend's active-only default, so tasks belonging to a completed
 * project silently dropped out of the count (see GtdPage.tsx, GoalCard.tsx).
 *
 * Two properties are locked down:
 *  1. Calling useProjects() with no arg MUST send byte-identical requests to
 *     before this change (no query string) — existing callers (DecisionsPage,
 *     emptyListDefence.test.ts) must not regress.
 *  2. The default fetch and the 'all' fetch MUST NOT share a TanStack Query
 *     cache entry — if `status` isn't part of the queryKey, one caller's
 *     fetch clobbers the other's cached data.
 */
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { renderHook, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import React from 'react'
import type { Project } from '../types/api'

import { useProjects } from './useProjects'

const apiFetchMock = vi.fn()
vi.mock('../lib/api', () => ({
  apiFetch: (...args: unknown[]) => apiFetchMock(...args),
}))

function makeProject(overrides: Partial<Project>): Project {
  return {
    id: 'p?',
    name: 'demo',
    title: 'Demo',
    status: 'active',
    area: 'engineering',
    priority: 3,
    created_at: '2026-04-01T00:00:00Z',
    updated_at: '2026-05-01T00:00:00Z',
    ...overrides,
  }
}

function makeWrapper(queryClient: QueryClient) {
  return ({ children }: { children: React.ReactNode }) =>
    React.createElement(QueryClientProvider, { client: queryClient }, children)
}

describe('useProjects', () => {
  beforeEach(() => {
    apiFetchMock.mockReset()
  })

  it('no-arg call sends no query string (byte-identical to pre-fix behaviour)', async () => {
    apiFetchMock.mockResolvedValueOnce([])
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })

    const { result } = renderHook(() => useProjects(), { wrapper: makeWrapper(client) })

    await waitFor(() => expect(result.current.isLoading).toBe(false))
    expect(apiFetchMock).toHaveBeenCalledTimes(1)
    expect(apiFetchMock).toHaveBeenCalledWith('/api/projects')
  })

  it("status='all' sends ?status=all", async () => {
    apiFetchMock.mockResolvedValueOnce([])
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })

    const { result } = renderHook(() => useProjects('all'), { wrapper: makeWrapper(client) })

    await waitFor(() => expect(result.current.isLoading).toBe(false))
    expect(apiFetchMock).toHaveBeenCalledTimes(1)
    expect(apiFetchMock).toHaveBeenCalledWith('/api/projects?status=all')
  })

  it("status='completed' sends ?status=completed", async () => {
    apiFetchMock.mockResolvedValueOnce([])
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })

    const { result } = renderHook(() => useProjects('completed'), { wrapper: makeWrapper(client) })

    await waitFor(() => expect(result.current.isLoading).toBe(false))
    expect(apiFetchMock).toHaveBeenCalledTimes(1)
    expect(apiFetchMock).toHaveBeenCalledWith('/api/projects?status=completed')
  })

  it("default fetch and 'all' fetch do not share a cache entry", async () => {
    const active = [makeProject({ id: 'p-active', status: 'active' })]
    const everything = [
      makeProject({ id: 'p-active', status: 'active' }),
      makeProject({ id: 'p-done', status: 'completed' }),
    ]
    // First call (whichever fires first below) resolves with `active`,
    // second resolves with `everything` — if both hooks were sharing one
    // cache entry, the second response would overwrite the first's `data`.
    apiFetchMock.mockResolvedValueOnce(active).mockResolvedValueOnce(everything)

    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    const wrapper = makeWrapper(client)

    const defaultHook = renderHook(() => useProjects(), { wrapper })
    const allHook = renderHook(() => useProjects('all'), { wrapper })

    await waitFor(() => expect(defaultHook.result.current.isLoading).toBe(false))
    await waitFor(() => expect(allHook.result.current.isLoading).toBe(false))

    // Both fetches actually went out — proves separate cache entries, not a
    // dedup on identical keys.
    expect(apiFetchMock).toHaveBeenCalledTimes(2)
    expect(apiFetchMock).toHaveBeenNthCalledWith(1, '/api/projects')
    expect(apiFetchMock).toHaveBeenNthCalledWith(2, '/api/projects?status=all')

    expect(defaultHook.result.current.data).toEqual(active)
    expect(allHook.result.current.data).toEqual(everything)
    // The discriminating assertion: the 'all' hook's data must include the
    // completed project that the default hook's data lacks.
    expect(allHook.result.current.data).toHaveLength(2)
    expect(defaultHook.result.current.data).toHaveLength(1)
  })

  it('data is [] when backend returns null, with status=all too (empty-list contract)', async () => {
    apiFetchMock.mockResolvedValueOnce(null)
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })

    const { result } = renderHook(() => useProjects('all'), { wrapper: makeWrapper(client) })

    await waitFor(() => expect(result.current.isLoading).toBe(false))
    expect(result.current.isError).toBe(false)
    expect(Array.isArray(result.current.data)).toBe(true)
    expect(result.current.data).toEqual([])
  })
})
