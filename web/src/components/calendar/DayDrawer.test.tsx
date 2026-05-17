import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { DayDrawer } from './DayDrawer'
import { ALL_KINDS } from './eventStyles'
import type { TimelineEvent, TimelineKind } from '../../types/api'

function renderInRouter(ui: React.ReactElement) {
  return render(<MemoryRouter>{ui}</MemoryRouter>)
}

function evt(kind: TimelineKind, occurredAt: string, title: string): TimelineEvent {
  return { kind, occurred_at: occurredAt, ref_id: `${kind}-${occurredAt}`, title }
}

describe('DayDrawer', () => {
  const allFilter = new Set<TimelineKind>(ALL_KINDS)

  it('does not render dialog content when day is null', () => {
    renderInRouter(
      <DayDrawer day={null} events={[]} kindFilter={allFilter} onClose={() => {}} />,
    )
    // The aside is always present (for animation), but headers/body only when day is set.
    const dialog = screen.getByRole('dialog')
    expect(dialog).toBeInTheDocument()
    // No close button when closed
    expect(screen.queryByRole('button', { name: 'Close day details' })).toBeNull()
  })

  it('renders the dialog with full date heading when day is set', () => {
    renderInRouter(
      <DayDrawer
        day={new Date(2025, 4, 15)}
        events={[]}
        kindFilter={allFilter}
        onClose={() => {}}
      />,
    )
    expect(screen.getByRole('button', { name: 'Close day details' })).toBeInTheDocument()
    expect(screen.getByText('No events on this day')).toBeInTheDocument()
  })

  it('groups events by kind and shows count per kind', () => {
    // Mid-day UTC timestamps for timezone-stable date bucketing.
    const events: TimelineEvent[] = [
      evt('task_created', '2025-05-15T12:00:00Z', 'Task A'),
      evt('task_created', '2025-05-15T12:30:00Z', 'Task B'),
      evt('decision',     '2025-05-15T13:00:00Z', 'Decision A'),
      evt('decision',     '2025-05-14T12:00:00Z', 'Other day'),
    ]
    renderInRouter(
      <DayDrawer
        day={new Date(2025, 4, 15)}
        events={events}
        kindFilter={allFilter}
        onClose={() => {}}
      />,
    )
    expect(screen.getByText('Task A')).toBeInTheDocument()
    expect(screen.getByText('Task B')).toBeInTheDocument()
    expect(screen.getByText('Decision A')).toBeInTheDocument()
    expect(screen.queryByText('Other day')).toBeNull()
    // "Task created (2)" header should appear
    expect(screen.getByText(/Task created \(2\)/)).toBeInTheDocument()
    expect(screen.getByText(/Decision \(1\)/)).toBeInTheDocument()
  })

  it('calls onClose when the close button is clicked', () => {
    const onClose = vi.fn()
    renderInRouter(
      <DayDrawer
        day={new Date(2025, 4, 15)}
        events={[]}
        kindFilter={allFilter}
        onClose={onClose}
      />,
    )
    fireEvent.click(screen.getByRole('button', { name: 'Close day details' }))
    expect(onClose).toHaveBeenCalledTimes(1)
  })

  it('calls onClose when ESC is pressed', () => {
    const onClose = vi.fn()
    renderInRouter(
      <DayDrawer
        day={new Date(2025, 4, 15)}
        events={[]}
        kindFilter={allFilter}
        onClose={onClose}
      />,
    )
    fireEvent.keyDown(document, { key: 'Escape' })
    expect(onClose).toHaveBeenCalledTimes(1)
  })

  it('does not call onClose on ESC when day is null', () => {
    const onClose = vi.fn()
    renderInRouter(
      <DayDrawer day={null} events={[]} kindFilter={allFilter} onClose={onClose} />,
    )
    fireEvent.keyDown(document, { key: 'Escape' })
    expect(onClose).not.toHaveBeenCalled()
  })

  it('returns focus to the triggering element when closed (WCAG 2.4.3)', () => {
    // Render a sibling trigger button + the closed drawer, focus the trigger,
    // then open and close the drawer. Focus MUST land back on the trigger.
    function Harness({ day }: { day: Date | null }) {
      return (
        <MemoryRouter>
          <button type="button" data-testid="trigger">
            Open
          </button>
          <DayDrawer day={day} events={[]} kindFilter={allFilter} onClose={() => {}} />
        </MemoryRouter>
      )
    }

    const { rerender } = render(<Harness day={null} />)
    const trigger = screen.getByTestId('trigger') as HTMLButtonElement
    trigger.focus()
    expect(document.activeElement).toBe(trigger)

    // Open: focus should move into the drawer (close button).
    rerender(<Harness day={new Date(2025, 4, 15)} />)
    expect(document.activeElement).toBe(
      screen.getByRole('button', { name: 'Close day details' }),
    )

    // Close: focus should return to the original trigger.
    rerender(<Harness day={null} />)
    expect(document.activeElement).toBe(trigger)
  })

  it('respects kindFilter when listing events for the day', () => {
    const events: TimelineEvent[] = [
      evt('task_created', '2025-05-15T12:00:00Z', 'Hidden'),
      evt('decision',     '2025-05-15T12:30:00Z', 'Visible'),
    ]
    const filter = new Set<TimelineKind>(['decision'])
    renderInRouter(
      <DayDrawer
        day={new Date(2025, 4, 15)}
        events={events}
        kindFilter={filter}
        onClose={() => {}}
      />,
    )
    expect(screen.queryByText('Hidden')).toBeNull()
    expect(screen.getByText('Visible')).toBeInTheDocument()
  })
})
