import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { WeekList } from './WeekList'
import { mondayOf } from './dateUtils'
import { ALL_KINDS } from './eventStyles'
import type { TimelineEvent, TimelineKind } from '../../types/api'

function evt(kind: TimelineKind, occurredAt: string, title: string): TimelineEvent {
  return { kind, occurred_at: occurredAt, ref_id: `${kind}-${occurredAt}`, title }
}

describe('mondayOf', () => {
  it('returns Monday for a Wednesday in the same week', () => {
    // 2025-05-14 is a Wednesday
    const m = mondayOf(new Date(2025, 4, 14))
    expect(m.getFullYear()).toBe(2025)
    expect(m.getMonth()).toBe(4)
    expect(m.getDate()).toBe(12) // Mon May 12, 2025
  })

  it('returns the same date when input is a Monday', () => {
    // 2025-05-12 is a Monday
    const m = mondayOf(new Date(2025, 4, 12))
    expect(m.getDate()).toBe(12)
  })

  it('returns the previous Monday when input is a Sunday', () => {
    // 2025-05-18 is a Sunday
    const m = mondayOf(new Date(2025, 4, 18))
    expect(m.getDate()).toBe(12)
  })
})

describe('WeekList', () => {
  const allFilter = new Set<TimelineKind>(ALL_KINDS)

  it('renders 7 day rows for the anchor week', () => {
    const { container } = render(
      <WeekList
        anchor={new Date(2025, 4, 14)} // Wed May 14
        events={[]}
        kindFilter={allFilter}
        onEventClick={() => {}}
      />,
    )
    const days = container.querySelectorAll('[data-testid="calendar-week-day"]')
    expect(days).toHaveLength(7)
    const keys = Array.from(days).map((d) => d.getAttribute('data-day'))
    expect(keys).toEqual([
      '2025-05-12',
      '2025-05-13',
      '2025-05-14',
      '2025-05-15',
      '2025-05-16',
      '2025-05-17',
      '2025-05-18',
    ])
  })

  it('groups events into the correct day section', () => {
    // Use mid-day UTC timestamps so the date stays stable across local
    // timezones (test machines may be UTC, JST, CST, etc).
    const events: TimelineEvent[] = [
      evt('task_created', '2025-05-13T12:00:00Z', 'Tue task'),
      evt('decision',     '2025-05-15T12:30:00Z', 'Thu decision'),
      evt('knowledge',    '2025-05-15T13:15:00Z', 'Thu knowledge'),
    ]
    const { container } = render(
      <WeekList
        anchor={new Date(2025, 4, 14)}
        events={events}
        kindFilter={allFilter}
        onEventClick={() => {}}
      />,
    )
    const tue = container.querySelector('[data-day="2025-05-13"]')!
    const thu = container.querySelector('[data-day="2025-05-15"]')!

    expect(tue.textContent).toContain('Tue task')
    expect(tue.textContent).not.toContain('Thu decision')

    expect(thu.textContent).toContain('Thu decision')
    expect(thu.textContent).toContain('Thu knowledge')
  })

  it('hides events whose kind is filtered out', () => {
    const events: TimelineEvent[] = [
      evt('task_created', '2025-05-15T08:00:00Z', 'Hidden'),
      evt('decision',     '2025-05-15T09:00:00Z', 'Visible'),
    ]
    const filter = new Set<TimelineKind>(['decision'])
    render(
      <WeekList
        anchor={new Date(2025, 4, 14)}
        events={events}
        kindFilter={filter}
        onEventClick={() => {}}
      />,
    )
    expect(screen.queryByText('Hidden')).toBeNull()
    expect(screen.getByText('Visible')).toBeInTheDocument()
  })

  it('invokes onEventClick when an event row is clicked', () => {
    const onEventClick = vi.fn()
    const events: TimelineEvent[] = [
      evt('decision', '2025-05-15T09:00:00Z', 'Click target'),
    ]
    render(
      <WeekList
        anchor={new Date(2025, 4, 14)}
        events={events}
        kindFilter={allFilter}
        onEventClick={onEventClick}
      />,
    )
    fireEvent.click(screen.getByText('Click target'))
    expect(onEventClick).toHaveBeenCalledTimes(1)
  })

  it('shows "No events" placeholder when a day has no events', () => {
    const { container } = render(
      <WeekList
        anchor={new Date(2025, 4, 14)}
        events={[]}
        kindFilter={allFilter}
        onEventClick={() => {}}
      />,
    )
    const tue = container.querySelector('[data-day="2025-05-13"]')!
    expect(tue.textContent).toContain('No events')
  })
})
