// [F1003-13] DecisionsPage's "load more" control has three branches driven
// by useDecisionsFeed — a load-more button, a distinct inline error+retry
// for next-page failures, and no control at all once there is no next page.
// A fourth branch (client-side search) must not hide the load-more button
// just because the current filter matched nothing. useDecisionsFeed is
// exercised via renderHook elsewhere and never renders JSX, so each branch
// is only covered by actually rendering the page.
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { DecisionsPage } from './DecisionsPage'
import type { Decision } from '../types/api'

const fetchNextPageMock = vi.fn()
const useDecisionsFeedMock = vi.fn()
const useProjectsMock = vi.fn()
const useReposMock = vi.fn()

vi.mock('../hooks/useDecisionsFeed', () => ({
  useDecisionsFeed: (...args: unknown[]) => useDecisionsFeedMock(...args),
}))
vi.mock('../hooks/useProjects', () => ({
  useProjects: (...args: unknown[]) => useProjectsMock(...args),
}))
vi.mock('../hooks/useRepos', () => ({
  useRepos: (...args: unknown[]) => useReposMock(...args),
}))

// DecisionsPage applies its own default date-range filter (today minus three
// months) on top of whatever useDecisionsFeed returns, so fixtures must fall
// inside that window regardless of when this test runs.
function daysAgo(n: number): string {
  const d = new Date()
  d.setDate(d.getDate() - n)
  return d.toISOString()
}

const decisionOne: Decision = {
  id: 'd1',
  title: 'Decision One',
  context: 'context one',
  decision: 'decided one',
  rationale: 'rationale one',
  created_at: daysAgo(1),
}
const decisionTwo: Decision = {
  id: 'd2',
  title: 'Decision Two',
  context: 'context two',
  decision: 'decided two',
  rationale: 'rationale two',
  created_at: daysAgo(2),
}

function mockFeed(overrides: Partial<ReturnType<typeof baseFeed>>) {
  useDecisionsFeedMock.mockReturnValue({ ...baseFeed(), ...overrides })
}

function baseFeed() {
  return {
    data: { pages: [{ decisions: [decisionOne, decisionTwo], offset: 0, limit: 20, has_more: true }] },
    isLoading: false,
    isError: false,
    hasNextPage: true,
    isFetchingNextPage: false,
    isFetchNextPageError: false,
    fetchNextPage: fetchNextPageMock,
  }
}

beforeEach(() => {
  fetchNextPageMock.mockReset()
  useProjectsMock.mockReturnValue({ data: [] })
  useReposMock.mockReturnValue({ data: [] })
})

describe('DecisionsPage load-more / next-page-error branches', () => {
  it('[F1003-13] shows the load-more button when hasNextPage is true and calls fetchNextPage on click', async () => {
    mockFeed({ hasNextPage: true, isFetchNextPageError: false })
    const user = userEvent.setup()
    render(<DecisionsPage />)

    const button = screen.getByRole('button', { name: 'Load more' })
    await user.click(button)

    expect(fetchNextPageMock).toHaveBeenCalledTimes(1)
  })

  it('[F1003-13] shows an alert with retry when isFetchNextPageError is true, keeping loaded rows visible', async () => {
    mockFeed({ hasNextPage: true, isFetchNextPageError: true })
    const user = userEvent.setup()
    render(<DecisionsPage />)

    const alert = screen.getByRole('alert')
    expect(alert).toHaveTextContent('Failed to load more decisions')

    const retryButton = screen.getByRole('button', { name: 'Retry' })
    await user.click(retryButton)

    expect(fetchNextPageMock).toHaveBeenCalledTimes(1)
    expect(screen.getByText('Decision One')).toBeInTheDocument()
  })

  it('[F1003-13] shows neither load-more nor retry when there is no next page and no error', () => {
    mockFeed({ hasNextPage: false, isFetchNextPageError: false })
    render(<DecisionsPage />)

    expect(screen.queryByRole('button', { name: 'Load more' })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Retry' })).not.toBeInTheDocument()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  it('[F1003-13] keeps the load-more button visible when the client-side search filters out every loaded row', async () => {
    mockFeed({ hasNextPage: true, isFetchNextPageError: false })
    const user = userEvent.setup()
    render(<DecisionsPage />)

    const search = screen.getByRole('textbox', { name: 'Search decisions...' })
    await user.type(search, 'no-such-decision-matches-this-query')

    expect(screen.queryByText('Decision One')).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Load more' })).toBeInTheDocument()
  })
})
