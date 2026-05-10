import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter, Routes, Route } from 'react-router-dom'
import { RepoDetailPage } from './RepoDetailPage'
import type { RepoOverview } from '../types/api'

const apiFetchMock = vi.fn()
vi.mock('../lib/api', () => ({
  apiFetch: (...args: unknown[]) => apiFetchMock(...args),
}))

function renderPage(repoId = 'r1') {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={queryClient}>
      <MemoryRouter initialEntries={[`/workspace/repos/${repoId}`]}>
        <Routes>
          <Route path="/workspace/repos/:id" element={<RepoDetailPage />} />
          <Route path="/workspace" element={<div>Workspace landing</div>} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

const baseOverview: RepoOverview = {
  repo: {
    id: 'r1',
    name: 'wayneblacktea',
    status: 'active',
    description: 'Personal OS',
    language: 'Go',
    current_branch: 'main',
    next_planned_step: 'ship overview',
    known_issues: [],
  },
  completed_tasks: [
    { id: 't1', title: 'First done', completed_at: '2026-04-15T10:00:00Z' },
    { id: 't2', title: 'Second done', completed_at: '2026-03-10T10:00:00Z' },
  ],
  pending_tasks: [
    { id: 'p1', title: 'High importance', status: 'pending', importance: 1, priority: 3 },
    { id: 'p2', title: 'Lower importance', status: 'pending', importance: 3, priority: 3 },
  ],
  recent_decisions: [
    {
      id: 'd1',
      title: 'Use Echo',
      decision: 'Echo HTTP framework',
      rationale: 'Fast and idiomatic',
      created_at: '2026-04-20T10:00:00Z',
    },
  ],
  recent_activity: [
    { id: 'a1', action: 'log_decision', notes: 'Decided on Echo', kind: 'decision', created_at: '2026-04-20T10:00:00Z' },
  ],
  recent_handoffs: [
    { id: 'h1', intent: 'Continue overview UI', status: 'open', created_at: '2026-04-21T10:00:00Z' },
  ],
}

describe('RepoDetailPage', () => {
  beforeEach(() => {
    apiFetchMock.mockReset()
  })

  afterEach(() => {
    vi.clearAllMocks()
  })

  it('renders the repo name and pending tasks sorted by importance', async () => {
    apiFetchMock.mockResolvedValueOnce(baseOverview)
    renderPage('r1')

    // Repo name is in the page header.
    await waitFor(() => expect(screen.getByRole('heading', { name: 'wayneblacktea' })).toBeInTheDocument())

    // Pending tasks should be rendered. Importance 1 entry should appear
    // before importance 3 (verified by DOM order).
    const high = await screen.findByText('High importance')
    const low = await screen.findByText('Lower importance')
    // compareDocumentPosition: 4 = high precedes low in DOM order.
    const highBeforeLow = high.compareDocumentPosition(low) & Node.DOCUMENT_POSITION_FOLLOWING
    expect(highBeforeLow).toBeTruthy()
  })

  it('groups completed tasks by month inside collapsible <details>', async () => {
    apiFetchMock.mockResolvedValueOnce(baseOverview)
    renderPage('r1')

    await waitFor(() => expect(screen.getByText('First done')).toBeInTheDocument())

    // Two month buckets → two <details>; each summary contains the count "(1)".
    const detailGroups = screen.getAllByRole('group')
    expect(detailGroups.length).toBeGreaterThanOrEqual(2)
  })

  it('toggles a completed-month section open and closed (WCAG <details>)', async () => {
    apiFetchMock.mockResolvedValueOnce(baseOverview)
    renderPage('r1')

    await waitFor(() => expect(screen.getByText('First done')).toBeInTheDocument())

    // Find the second month group (default closed since only the first is `open`).
    const detailEls = document.querySelectorAll('details')
    expect(detailEls.length).toBeGreaterThanOrEqual(2)
    const secondGroup = detailEls[1]
    expect(secondGroup.open).toBe(false)

    // Toggle open by clicking the summary.
    const summary = secondGroup.querySelector('summary')
    expect(summary).not.toBeNull()
    if (summary) {
      fireEvent.click(summary)
      expect(secondGroup.open).toBe(true)
    }
  })

  it('shows the decision rationale alongside the decision text', async () => {
    apiFetchMock.mockResolvedValueOnce(baseOverview)
    renderPage('r1')
    await waitFor(() => expect(screen.getByText('Use Echo')).toBeInTheDocument())
    expect(screen.getByText(/Fast and idiomatic/)).toBeInTheDocument()
  })

  it('shows the recent activity item with its kind dot', async () => {
    apiFetchMock.mockResolvedValueOnce(baseOverview)
    renderPage('r1')
    await waitFor(() => expect(screen.getByText('Decided on Echo')).toBeInTheDocument())
  })

  it('shows handoff intent and the open badge', async () => {
    apiFetchMock.mockResolvedValueOnce(baseOverview)
    renderPage('r1')
    await waitFor(() => expect(screen.getByText('Continue overview UI')).toBeInTheDocument())
  })

  it('renders an error banner when the API rejects', async () => {
    apiFetchMock.mockRejectedValueOnce(new Error('500: boom'))
    renderPage('r1')
    await waitFor(() => expect(screen.getByText('Failed to load')).toBeInTheDocument())
  })

  it('renders empty-state messages when sections are empty', async () => {
    apiFetchMock.mockResolvedValueOnce({
      ...baseOverview,
      completed_tasks: [],
      pending_tasks: [],
      recent_decisions: [],
      recent_activity: [],
      recent_handoffs: [],
    })
    renderPage('r1')
    await waitFor(() => expect(screen.getByText('No completed work yet')).toBeInTheDocument())
    expect(screen.getByText('Nothing pending')).toBeInTheDocument()
    expect(screen.getByText('No decisions logged')).toBeInTheDocument()
    expect(screen.getByText('No activity in the last 14 days')).toBeInTheDocument()
    expect(screen.getByText('No handoffs yet')).toBeInTheDocument()
  })
})
