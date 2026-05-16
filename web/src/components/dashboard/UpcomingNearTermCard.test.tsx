import { describe, it, expect, vi, afterEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { UpcomingNearTermCard, formatDueDate } from './UpcomingNearTermCard'

vi.mock('../../hooks/useDashboardUpcoming', () => ({
  useDashboardUpcoming: vi.fn(),
}))

import { useDashboardUpcoming } from '../../hooks/useDashboardUpcoming'
const mockUseUpcoming = vi.mocked(useDashboardUpcoming)

function renderCard() {
  return render(
    <MemoryRouter>
      <UpcomingNearTermCard />
    </MemoryRouter>,
  )
}

afterEach(() => {
  vi.restoreAllMocks()
})

// ---- formatDueDate pure-function tests ----

describe('formatDueDate', () => {
  it('returns "Today" for the same calendar day', () => {
    const now = new Date()
    const sameDay = new Date(now.getFullYear(), now.getMonth(), now.getDate(), 9, 0, 0).toISOString()
    expect(formatDueDate(sameDay)).toBe('Today')
  })

  it('returns "Tomorrow" for next calendar day', () => {
    const tomorrow = new Date()
    tomorrow.setDate(tomorrow.getDate() + 1)
    const tomorrowNoon = new Date(
      tomorrow.getFullYear(),
      tomorrow.getMonth(),
      tomorrow.getDate(),
      12,
      0,
      0,
    ).toISOString()
    expect(formatDueDate(tomorrowNoon)).toBe('Tomorrow')
  })

  it('returns locale short date for dates further out', () => {
    // Use a fixed date 7 days ahead so the result is deterministic
    const future = new Date()
    future.setDate(future.getDate() + 7)
    const result = formatDueDate(future.toISOString())
    // Should contain the month abbreviation and day number, not "Today"/"Tomorrow"
    expect(result).not.toBe('Today')
    expect(result).not.toBe('Tomorrow')
    // A locale short date like "Jun 3" contains a space
    expect(result.length).toBeGreaterThan(3)
  })
})

// ---- UpcomingNearTermCard component tests ----

describe('UpcomingNearTermCard', () => {
  it('renders loading skeletons when isLoading is true', () => {
    mockUseUpcoming.mockReturnValue({ data: undefined, isLoading: true } as ReturnType<typeof useDashboardUpcoming>)
    const { container } = renderCard()
    // Loading state renders LoadingSkeleton divs (no role=list)
    expect(container.querySelector('[role="list"]')).toBeNull()
    expect(screen.queryByText(/No tasks/i)).toBeNull()
  })

  it('renders empty state when data is an empty array', () => {
    mockUseUpcoming.mockReturnValue({ data: [], isLoading: false } as ReturnType<typeof useDashboardUpcoming>)
    renderCard()
    expect(screen.queryByRole('list')).toBeNull()
    // EmptyState renders the noNearTermTasks message key
    expect(screen.getByText('No tasks due in the next 7 days')).toBeInTheDocument()
  })

  it('renders task list when data is present', () => {
    const tomorrow = new Date()
    tomorrow.setDate(tomorrow.getDate() + 1)
    const tasks = [
      { id: 'task-1', title: 'Ship MCP-3 vagueness', due_date: tomorrow.toISOString(), status: 'in_progress', priority: 1 },
      { id: 'task-2', title: 'Review PR #109', due_date: tomorrow.toISOString(), status: 'pending', priority: 2 },
    ]
    mockUseUpcoming.mockReturnValue({ data: tasks, isLoading: false } as ReturnType<typeof useDashboardUpcoming>)
    renderCard()

    expect(screen.getByRole('list')).toBeInTheDocument()
    expect(screen.getByText('Ship MCP-3 vagueness')).toBeInTheDocument()
    expect(screen.getByText('Review PR #109')).toBeInTheDocument()
    // Both tasks are due tomorrow → "Tomorrow" label
    expect(screen.getAllByText('Tomorrow')).toHaveLength(2)
  })

  it('caps display at MAX_TASKS=5 when more tasks are provided', () => {
    const now = new Date()
    const tasks = Array.from({ length: 8 }, (_, i) => ({
      id: `task-${i}`,
      title: `Task ${i + 1}`,
      due_date: new Date(now.getFullYear(), now.getMonth(), now.getDate(), 10).toISOString(),
      status: 'pending',
      priority: i + 1,
    }))
    mockUseUpcoming.mockReturnValue({ data: tasks, isLoading: false } as ReturnType<typeof useDashboardUpcoming>)
    renderCard()

    const items = screen.getAllByRole('listitem')
    expect(items).toHaveLength(5)
  })

  it('handles null/undefined data gracefully (data ?? [] guard)', () => {
    mockUseUpcoming.mockReturnValue({ data: undefined, isLoading: false } as ReturnType<typeof useDashboardUpcoming>)
    renderCard()
    // data ?? [] → tasks = [] → empty state, no crash
    expect(screen.getByText('No tasks due in the next 7 days')).toBeInTheDocument()
  })

  it('each task row links to /gtd?task_id=<uuid>', () => {
    const tomorrow = new Date()
    tomorrow.setDate(tomorrow.getDate() + 1)
    const tasks = [
      { id: 'abc-123', title: 'Fix login', due_date: tomorrow.toISOString(), status: 'pending', priority: 1 },
    ]
    mockUseUpcoming.mockReturnValue({ data: tasks, isLoading: false } as ReturnType<typeof useDashboardUpcoming>)
    renderCard()

    const link = screen.getByText('Fix login').closest('a')
    expect(link).toHaveAttribute('href', '/gtd?task_id=abc-123')
  })
})
