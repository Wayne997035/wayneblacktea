import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { MonthGrid } from './MonthGrid'
import { buildMonthMatrix } from './dateUtils'
import { ALL_KINDS } from './eventStyles'
import type { TimelineEvent, TimelineKind } from '../../types/api'

describe('buildMonthMatrix', () => {
  it('returns 6 rows × 7 columns (42 cells)', () => {
    const matrix = buildMonthMatrix(new Date(2025, 4, 15)) // May 2025
    expect(matrix).toHaveLength(6)
    for (const row of matrix) {
      expect(row).toHaveLength(7)
    }
  })

  it('first cell is the Monday on/before the 1st of the month', () => {
    // May 2025: 1st is a Thursday → Monday before = Apr 28
    const matrix = buildMonthMatrix(new Date(2025, 4, 15))
    const first = matrix[0][0]
    expect(first.getFullYear()).toBe(2025)
    expect(first.getMonth()).toBe(3) // April
    expect(first.getDate()).toBe(28)
    // Verify it's a Monday: JS getDay() Monday = 1
    expect(first.getDay()).toBe(1)
  })

  it('handles a month that starts on Monday cleanly', () => {
    // Sep 2025: 1st is a Monday
    const matrix = buildMonthMatrix(new Date(2025, 8, 15))
    expect(matrix[0][0].getMonth()).toBe(8)
    expect(matrix[0][0].getDate()).toBe(1)
    expect(matrix[0][0].getDay()).toBe(1)
  })

  it('last cell is a Sunday', () => {
    const matrix = buildMonthMatrix(new Date(2025, 4, 15))
    const last = matrix[5][6]
    // Sunday = 0
    expect(last.getDay()).toBe(0)
  })
})

function evt(kind: TimelineKind, occurredAt: string, title = `${kind} title`): TimelineEvent {
  return { kind, occurred_at: occurredAt, ref_id: `${kind}-${occurredAt}`, title }
}

describe('MonthGrid', () => {
  const allFilter = new Set<TimelineKind>(ALL_KINDS)
  const anchor = new Date(2025, 4, 15) // May 2025

  it('renders 42 day cells', () => {
    render(
      <MonthGrid
        anchor={anchor}
        events={[]}
        kindFilter={allFilter}
        onDayClick={() => {}}
      />,
    )
    const cells = screen.getAllByTestId('calendar-day-cell')
    expect(cells).toHaveLength(42)
  })

  it('renders one dot per distinct kind on a day', () => {
    // Use mid-day UTC (T12:00:00Z) so the date stays stable across local
    // timezones (CI may be UTC, JST, CST, etc).
    const events: TimelineEvent[] = [
      evt('task_created',    '2025-05-15T12:00:00Z'),
      evt('task_completed',  '2025-05-15T12:30:00Z'),
      evt('decision',        '2025-05-15T13:00:00Z'),
      // Duplicate kind on same day → should NOT add another dot
      evt('decision',        '2025-05-15T13:30:00Z'),
    ]
    const { container } = render(
      <MonthGrid
        anchor={anchor}
        events={events}
        kindFilter={allFilter}
        onDayClick={() => {}}
      />,
    )
    const target = container.querySelector('[data-day="2025-05-15"]')
    expect(target).not.toBeNull()
    const dots = target!.querySelectorAll('span.rounded-full')
    // task_created + task_completed share bg-green-500 but they're different
    // kinds → 3 distinct kinds = 3 dots.
    expect(dots.length).toBe(3)
  })

  it('shows +N overflow when more than 4 kinds on a day', () => {
    // All timestamps are T12:00:00Z (mid-day UTC) for timezone stability.
    const events: TimelineEvent[] = [
      evt('task_created',     '2025-05-15T12:00:00Z'),
      evt('task_completed',   '2025-05-15T12:10:00Z'),
      evt('decision',         '2025-05-15T12:20:00Z'),
      evt('knowledge',        '2025-05-15T12:30:00Z'),
      evt('review_submitted', '2025-05-15T12:40:00Z'),
      evt('handoff_created',  '2025-05-15T12:50:00Z'),
    ]
    const { container } = render(
      <MonthGrid
        anchor={anchor}
        events={events}
        kindFilter={allFilter}
        onDayClick={() => {}}
      />,
    )
    const target = container.querySelector('[data-day="2025-05-15"]')!
    expect(target.textContent).toMatch(/\+2/)
  })

  it('respects kindFilter — filtered kinds are not rendered as dots', () => {
    const events: TimelineEvent[] = [
      evt('task_created', '2025-05-15T12:00:00Z'),
      evt('decision',     '2025-05-15T12:30:00Z'),
    ]
    const filter = new Set<TimelineKind>(['decision'])
    const { container } = render(
      <MonthGrid
        anchor={anchor}
        events={events}
        kindFilter={filter}
        onDayClick={() => {}}
      />,
    )
    const target = container.querySelector('[data-day="2025-05-15"]')!
    const dots = target.querySelectorAll('span.rounded-full')
    expect(dots.length).toBe(1) // only decision
  })

  it('invokes onDayClick with the cell date when clicked', () => {
    const onDayClick = vi.fn()
    const { container } = render(
      <MonthGrid
        anchor={anchor}
        events={[]}
        kindFilter={allFilter}
        onDayClick={onDayClick}
      />,
    )
    const cell = container.querySelector('[data-day="2025-05-15"]') as HTMLElement
    fireEvent.click(cell)
    expect(onDayClick).toHaveBeenCalledTimes(1)
    const arg = onDayClick.mock.calls[0][0] as Date
    expect(arg.getFullYear()).toBe(2025)
    expect(arg.getMonth()).toBe(4)
    expect(arg.getDate()).toBe(15)
  })
})
