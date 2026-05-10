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

  it('renders one text chip per event on a day (not per kind)', () => {
    // Two events of the same kind both produce two chips — chips are
    // per-event, unlike the legacy per-kind dot summary.
    const events: TimelineEvent[] = [
      evt('decision', '2025-05-15T13:00:00Z', 'first decision'),
      evt('decision', '2025-05-15T13:30:00Z', 'second decision'),
      evt('task_created', '2025-05-15T14:00:00Z', 'task A'),
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
    const chips = target.querySelectorAll('[data-testid="calendar-event-chip"]')
    expect(chips.length).toBe(3)
  })

  it('truncates chip text to ~20 chars with ellipsis', () => {
    const events: TimelineEvent[] = [
      evt('decision', '2025-05-15T12:00:00Z', 'this is a really really long decision title'),
    ]
    const { container } = render(
      <MonthGrid
        anchor={anchor}
        events={events}
        kindFilter={allFilter}
        onDayClick={() => {}}
      />,
    )
    const chip = container.querySelector(
      '[data-day="2025-05-15"] [data-testid="calendar-event-chip"]',
    )!
    // Chip displays both the dot (aria-hidden) and the truncated label.
    expect(chip.textContent ?? '').toMatch(/…$/)
    // Length excluding the dot's empty label should be ≤20.
    expect((chip.textContent ?? '').trim().length).toBeLessThanOrEqual(20)
  })

  it('shows "+N more" hint when day has more than 3 events', () => {
    const events: TimelineEvent[] = Array.from({ length: 6 }, (_, i) =>
      evt('decision', `2025-05-15T1${i}:00:00Z`, `event ${i}`),
    )
    const { container } = render(
      <MonthGrid
        anchor={anchor}
        events={events}
        kindFilter={allFilter}
        onDayClick={() => {}}
      />,
    )
    const target = container.querySelector('[data-day="2025-05-15"]')!
    const chips = target.querySelectorAll('[data-testid="calendar-event-chip"]')
    expect(chips.length).toBe(3)
    const more = target.querySelector('[data-testid="calendar-more-button"]')
    expect(more).not.toBeNull()
    expect(more!.textContent ?? '').toMatch(/3/)
  })

  it('does NOT render +N more when exactly 3 events fit', () => {
    const events: TimelineEvent[] = Array.from({ length: 3 }, (_, i) =>
      evt('decision', `2025-05-15T1${i}:00:00Z`, `event ${i}`),
    )
    const { container } = render(
      <MonthGrid
        anchor={anchor}
        events={events}
        kindFilter={allFilter}
        onDayClick={() => {}}
      />,
    )
    const target = container.querySelector('[data-day="2025-05-15"]')!
    expect(target.querySelector('[data-testid="calendar-more-button"]')).toBeNull()
  })

  it('renders future task_due chips with data-planned=true (dashed outline)', () => {
    const future = new Date()
    future.setDate(future.getDate() + 2) // 2 days from now
    const events: TimelineEvent[] = [
      evt('task_due', future.toISOString(), 'planned work'),
    ]
    // Anchor on the future date so the cell is in view.
    const futureAnchor = new Date(
      future.getFullYear(),
      future.getMonth(),
      future.getDate(),
    )
    const { container } = render(
      <MonthGrid
        anchor={futureAnchor}
        events={events}
        kindFilter={allFilter}
        onDayClick={() => {}}
      />,
    )
    const futureKey = `${future.getFullYear()}-${String(future.getMonth() + 1).padStart(2, '0')}-${String(future.getDate()).padStart(2, '0')}`
    const target = container.querySelector(`[data-day="${futureKey}"]`)!
    const chip = target.querySelector('[data-testid="calendar-event-chip"]')
    expect(chip).not.toBeNull()
    expect(chip!.getAttribute('data-planned')).toBe('true')
  })

  it('renders past task_due chips with data-planned=false (solid)', () => {
    const past = new Date()
    past.setDate(past.getDate() - 2)
    const events: TimelineEvent[] = [
      evt('task_due', past.toISOString(), 'overdue work'),
    ]
    const pastAnchor = new Date(past.getFullYear(), past.getMonth(), past.getDate())
    const { container } = render(
      <MonthGrid
        anchor={pastAnchor}
        events={events}
        kindFilter={allFilter}
        onDayClick={() => {}}
      />,
    )
    const pastKey = `${past.getFullYear()}-${String(past.getMonth() + 1).padStart(2, '0')}-${String(past.getDate()).padStart(2, '0')}`
    const target = container.querySelector(`[data-day="${pastKey}"]`)!
    const chip = target.querySelector('[data-testid="calendar-event-chip"]')
    expect(chip).not.toBeNull()
    expect(chip!.getAttribute('data-planned')).toBe('false')
  })

  it('respects kindFilter — filtered kinds are not rendered as chips', () => {
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
    const chips = target.querySelectorAll('[data-testid="calendar-event-chip"]')
    expect(chips.length).toBe(1)
  })

  it('chips have aria-label including kind label and title', () => {
    const events: TimelineEvent[] = [
      evt('task_due', '2025-05-15T12:00:00Z', 'review backend rules'),
    ]
    const { container } = render(
      <MonthGrid
        anchor={anchor}
        events={events}
        kindFilter={allFilter}
        onDayClick={() => {}}
      />,
    )
    const chip = container.querySelector(
      '[data-day="2025-05-15"] [data-testid="calendar-event-chip"]',
    )!
    const label = chip.getAttribute('aria-label') ?? ''
    expect(label).toContain('review backend rules')
    // Either Chinese or English kind label resolves; both contain text.
    expect(label.length).toBeGreaterThan('review backend rules'.length)
  })

  it('+N more hint has aria-label that announces remaining count', () => {
    const events: TimelineEvent[] = Array.from({ length: 5 }, (_, i) =>
      evt('decision', `2025-05-15T1${i}:00:00Z`, `event ${i}`),
    )
    const { container } = render(
      <MonthGrid
        anchor={anchor}
        events={events}
        kindFilter={allFilter}
        onDayClick={() => {}}
      />,
    )
    const more = container.querySelector(
      '[data-day="2025-05-15"] [data-testid="calendar-more-button"]',
    )!
    const label = more.getAttribute('aria-label') ?? ''
    expect(label).toMatch(/2/) // 5 events - 3 shown = 2 more
  })

  it('mobile dots region renders distinct kind dots for the visible chips', () => {
    const events: TimelineEvent[] = [
      evt('task_created',     '2025-05-15T10:00:00Z'),
      evt('decision',         '2025-05-15T11:00:00Z'),
      evt('knowledge',        '2025-05-15T12:00:00Z'),
      evt('review_submitted', '2025-05-15T13:00:00Z'),
    ]
    const { container } = render(
      <MonthGrid
        anchor={anchor}
        events={events}
        kindFilter={allFilter}
        onDayClick={() => {}}
      />,
    )
    const dots = container.querySelector(
      '[data-day="2025-05-15"] [data-testid="calendar-day-dots"]',
    )
    expect(dots).not.toBeNull()
    const dotElements = dots!.querySelectorAll('span.rounded-full')
    // 3 shown chips → 3 distinct kinds in the mobile preview (4th event becomes +1).
    expect(dotElements.length).toBe(3)
    expect(dots!.textContent ?? '').toMatch(/\+1/)
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
