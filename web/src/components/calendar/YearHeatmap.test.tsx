import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { YearHeatmap } from './YearHeatmap'
import type { TimelineEvent, TimelineKind } from '../../types/api'

function evt(kind: TimelineKind, occurredAt: string, title = `${kind} title`): TimelineEvent {
  return { kind, occurred_at: occurredAt, ref_id: `${kind}-${occurredAt}`, title }
}

describe('YearHeatmap', () => {
  it('renders one rect per day in the year (365 in 2025)', () => {
    render(<YearHeatmap year={2025} events={[]} onMonthJump={() => {}} />)
    const cells = screen.getAllByTestId('heatmap-cell')
    expect(cells).toHaveLength(365)
  })

  it('renders 366 cells for leap year 2024', () => {
    render(<YearHeatmap year={2024} events={[]} onMonthJump={() => {}} />)
    const cells = screen.getAllByTestId('heatmap-cell')
    expect(cells).toHaveLength(366)
  })

  it('cells have ascending bucket data when events accumulate', () => {
    const events: TimelineEvent[] = [
      evt('task_created',   '2025-05-15T12:00:00Z'),
      evt('task_completed', '2025-05-15T13:00:00Z'),
      evt('decision',       '2025-05-15T14:00:00Z'),
    ]
    const { container } = render(
      <YearHeatmap year={2025} events={events} onMonthJump={() => {}} />,
    )
    const target = container.querySelector('[data-day="2025-05-15"]')
    expect(target).not.toBeNull()
    expect(target!.getAttribute('data-bucket')).toBe('2') // 3 events → bucket 2
  })

  it('clicking a cell calls onMonthJump with the first day of that cell\'s month', () => {
    const onMonthJump = vi.fn()
    const { container } = render(
      <YearHeatmap year={2025} events={[]} onMonthJump={onMonthJump} />,
    )
    const target = container.querySelector('[data-day="2025-05-15"]') as SVGRectElement
    expect(target).not.toBeNull()
    fireEvent.click(target)
    expect(onMonthJump).toHaveBeenCalledTimes(1)
    const arg = onMonthJump.mock.calls[0][0] as Date
    expect(arg.getFullYear()).toBe(2025)
    expect(arg.getMonth()).toBe(4) // May
    expect(arg.getDate()).toBe(1) // first day of month
  })

  it('Enter on a focused cell triggers onMonthJump', () => {
    const onMonthJump = vi.fn()
    const { container } = render(
      <YearHeatmap year={2025} events={[]} onMonthJump={onMonthJump} />,
    )
    const target = container.querySelector('[data-day="2025-07-04"]') as SVGRectElement
    fireEvent.keyDown(target, { key: 'Enter' })
    expect(onMonthJump).toHaveBeenCalledTimes(1)
    const arg = onMonthJump.mock.calls[0][0] as Date
    expect(arg.getMonth()).toBe(6) // July
    expect(arg.getDate()).toBe(1)
  })

  it('each cell has an aria-label combining date + event count', () => {
    const events: TimelineEvent[] = [evt('task_created', '2025-05-15T12:00:00Z')]
    const { container } = render(
      <YearHeatmap year={2025} events={events} onMonthJump={() => {}} />,
    )
    const target = container.querySelector('[data-day="2025-05-15"]') as SVGRectElement
    const label = target.getAttribute('aria-label')
    expect(label).toMatch(/1 event/)
  })

  it('renders an SVG title (tooltip) for each in-year cell', () => {
    const { container } = render(
      <YearHeatmap year={2025} events={[]} onMonthJump={() => {}} />,
    )
    const titles = container.querySelectorAll('rect[data-testid="heatmap-cell"] title')
    expect(titles.length).toBe(365)
  })
})
