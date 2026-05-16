import { describe, it, expect, vi, afterEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import { RecentAutomationFeedCard } from './RecentAutomationFeedCard'

vi.mock('../../hooks/useAutomationFeed', () => ({
  useAutomationFeed: vi.fn(),
}))

import { useAutomationFeed } from '../../hooks/useAutomationFeed'
const mockUseAutomationFeed = vi.mocked(useAutomationFeed)

afterEach(() => {
  vi.restoreAllMocks()
})

const baseFeedItem = {
  id: 'abc-1',
  action: 'task:completed',
  kind: 'task_completed',
  occurred_at: new Date(Date.now() - 60_000).toISOString(),
}

describe('RecentAutomationFeedCard', () => {
  it('renders loading skeletons when isLoading is true', () => {
    mockUseAutomationFeed.mockReturnValue({
      data: undefined,
      isLoading: true,
      isError: false,
    } as unknown as ReturnType<typeof useAutomationFeed>)
    const { container } = render(<RecentAutomationFeedCard />)
    // Loading: no feed rows visible, skeletons present
    expect(screen.queryByText('task:completed')).toBeNull()
    // At least one child in the container (skeleton divs)
    expect(container.firstChild).not.toBeNull()
  })

  it('renders empty state when feed is empty', () => {
    mockUseAutomationFeed.mockReturnValue({
      data: { feed: [], fetched_at: new Date().toISOString() },
      isLoading: false,
      isError: false,
    } as unknown as ReturnType<typeof useAutomationFeed>)
    render(<RecentAutomationFeedCard />)
    expect(screen.getByText('MCP Automation Feed')).toBeInTheDocument()
  })

  it('renders feed rows when data is present', () => {
    const items = [
      { ...baseFeedItem, id: 'item-1', notes: 'Completed task XYZ' },
      { ...baseFeedItem, id: 'item-2', action: 'decision:logged', kind: 'decision', notes: 'Decided on Redis' },
    ]
    mockUseAutomationFeed.mockReturnValue({
      data: { feed: items, fetched_at: new Date().toISOString() },
      isLoading: false,
      isError: false,
    } as unknown as ReturnType<typeof useAutomationFeed>)
    render(<RecentAutomationFeedCard />)
    expect(screen.getByText('Completed task XYZ')).toBeInTheDocument()
    expect(screen.getByText('Decided on Redis')).toBeInTheDocument()
  })

  it('renders error state when isError is true', () => {
    mockUseAutomationFeed.mockReturnValue({
      data: undefined,
      isLoading: false,
      isError: true,
    } as unknown as ReturnType<typeof useAutomationFeed>)
    render(<RecentAutomationFeedCard />)
    expect(screen.getByText('MCP Automation Feed')).toBeInTheDocument()
  })

  it('falls back to action text when notes is absent', () => {
    const item = { id: 'item-no-notes', action: 'dashboard:reconciled', kind: 'activity', occurred_at: new Date().toISOString() }
    mockUseAutomationFeed.mockReturnValue({
      data: { feed: [item], fetched_at: new Date().toISOString() },
      isLoading: false,
      isError: false,
    } as unknown as ReturnType<typeof useAutomationFeed>)
    render(<RecentAutomationFeedCard />)
    // With no notes, should fall back to action
    expect(screen.getByText('dashboard:reconciled')).toBeInTheDocument()
  })
})
