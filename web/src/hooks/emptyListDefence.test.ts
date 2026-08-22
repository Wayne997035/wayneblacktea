/**
 * Cross-cutting contract test: every list-returning query hook MUST guarantee
 * an array even when the backend responds with JSON `null` for an empty list
 * (sqlite nil-slice → JSON null is a known backend behaviour). The guard
 * lives in each hook's
 * `select: (data) => data ?? []`, mirroring the pre-existing pattern in
 * useTasks.ts (`useTasksByProject` / `useAllTasks`).
 *
 * Each `it()` below is a mutation-testable proof for exactly one hook:
 * mock the raw fetch response as `null`, then assert the hook's returned
 * `data` is an array (never assert on `.length` alone — `null` and `[]`
 * both satisfy a `.length === 0` check under some call patterns, which
 * would give the assertion zero discriminating power).
 */
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { renderHook, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import React from 'react'

import { useDecisions } from './useDecisions'
import { usePendingProposals, useAllProposals } from './usePendingProposals'
import { useNewConcepts, useReviews } from './useReviews'
import { useVision } from './useVision'
import { useGoals } from './useGoals'
import { useProjects } from './useProjects'
import { useRepos } from './useRepos'
import { useDueReviewsDashboard } from './useDueReviewsDashboard'
import { useDashboardRecentDecisions } from './useDashboardRecentDecisions'
import { useRecentKnowledge } from './useRecentKnowledge'
import { useLearningHistory } from './useLearningHistory'
import { useKnowledge, useKnowledgeSearch } from './useKnowledge'

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

/** Renders `useHook()`, waits for settle, and asserts the returned data is a real array. */
async function expectArrayOnNull<T>(useHook: () => { data?: T; isLoading: boolean; isError: boolean }) {
  apiFetchMock.mockResolvedValueOnce(null)
  const { result } = renderHook(useHook, { wrapper: makeWrapper() })

  await waitFor(() => expect(result.current.isLoading).toBe(false))
  expect(result.current.isError).toBe(false)
  expect(Array.isArray(result.current.data)).toBe(true)
  expect(result.current.data).toEqual([])
  return result
}

describe('hook-layer empty-list defence (backend returns raw null)', () => {
  beforeEach(() => {
    apiFetchMock.mockReset()
  })

  it('useDecisions: data is [] when backend returns null', async () => {
    await expectArrayOnNull(() => useDecisions())
  })

  it('usePendingProposals: data is [] when backend returns null', async () => {
    await expectArrayOnNull(() => usePendingProposals())
  })

  it('useAllProposals: data is [] when backend returns null', async () => {
    await expectArrayOnNull(() => useAllProposals('accepted'))
  })

  it('useNewConcepts: data is [] when backend returns null (queryFn .filter is null-guarded too)', async () => {
    await expectArrayOnNull(() => useNewConcepts())
  })

  it('useReviews: data is [] when backend returns null', async () => {
    await expectArrayOnNull(() => useReviews())
  })

  it('useVision: data is [] when backend returns null', async () => {
    await expectArrayOnNull(() => useVision())
  })

  it('useGoals: data is [] when backend returns null', async () => {
    await expectArrayOnNull(() => useGoals())
  })

  it('useProjects: data is [] when backend returns null', async () => {
    await expectArrayOnNull(() => useProjects())
  })

  it('useRepos: data is [] when backend returns null', async () => {
    await expectArrayOnNull(() => useRepos())
  })

  it('useDueReviewsDashboard: data is [] when backend returns null', async () => {
    await expectArrayOnNull(() => useDueReviewsDashboard())
  })

  it('useDashboardRecentDecisions: data is [] when backend returns null', async () => {
    await expectArrayOnNull(() => useDashboardRecentDecisions())
  })

  it('useRecentKnowledge: data is [] when backend returns null', async () => {
    await expectArrayOnNull(() => useRecentKnowledge())
  })

  it('useLearningHistory: data is [] when backend returns null', async () => {
    await expectArrayOnNull(() => useLearningHistory())
  })

  it('useKnowledge: data is [] when backend returns null', async () => {
    await expectArrayOnNull(() => useKnowledge())
  })

  it('useKnowledgeSearch: data is [] when backend returns null', async () => {
    await expectArrayOnNull(() => useKnowledgeSearch('anything'))
  })
})
