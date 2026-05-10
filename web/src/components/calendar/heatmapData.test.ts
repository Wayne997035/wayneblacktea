import { describe, it, expect } from 'vitest'
import { bucketForCount, buildYearHeatmap, SENTINEL_COUNT } from './heatmapData'
import type { TimelineEvent, TimelineKind } from '../../types/api'

describe('bucketForCount', () => {
  it('maps 0 to bucket 0', () => {
    expect(bucketForCount(0)).toBe(0)
  })

  it('maps 1..2 to bucket 1', () => {
    expect(bucketForCount(1)).toBe(1)
    expect(bucketForCount(2)).toBe(1)
  })

  it('maps 3..5 to bucket 2', () => {
    expect(bucketForCount(3)).toBe(2)
    expect(bucketForCount(4)).toBe(2)
    expect(bucketForCount(5)).toBe(2)
  })

  it('maps 6..10 to bucket 3', () => {
    expect(bucketForCount(6)).toBe(3)
    expect(bucketForCount(10)).toBe(3)
  })

  it('maps 11+ to bucket 4', () => {
    expect(bucketForCount(11)).toBe(4)
    expect(bucketForCount(100)).toBe(4)
    expect(bucketForCount(10_000)).toBe(4)
  })

  it('treats negative counts as 0 (defensive)', () => {
    expect(bucketForCount(-1)).toBe(0)
  })
})

function evt(kind: TimelineKind, occurredAt: string, title = `${kind} title`): TimelineEvent {
  return { kind, occurred_at: occurredAt, ref_id: `${kind}-${occurredAt}`, title }
}

describe('buildYearHeatmap', () => {
  it('returns 7 rows', () => {
    const grid = buildYearHeatmap([], 2025)
    expect(grid).toHaveLength(7)
  })

  it('every row has the same number of columns (52 or 53)', () => {
    const grid = buildYearHeatmap([], 2025)
    const cols = grid[0].length
    expect(cols === 52 || cols === 53 || cols === 54).toBe(true)
    for (const row of grid) {
      expect(row).toHaveLength(cols)
    }
  })

  it('contains 365 (or 366) in-year cells exactly', () => {
    const grid = buildYearHeatmap([], 2025)
    const inYearCells = grid.flat().filter((c) => c.count !== SENTINEL_COUNT)
    expect(inYearCells.length).toBe(365) // 2025 is not a leap year
  })

  it('contains 366 in-year cells for leap year 2024', () => {
    const grid = buildYearHeatmap([], 2024)
    const inYearCells = grid.flat().filter((c) => c.count !== SENTINEL_COUNT)
    expect(inYearCells.length).toBe(366)
  })

  it('marks out-of-year cells with SENTINEL_COUNT (-1)', () => {
    const grid = buildYearHeatmap([], 2025)
    const sentinels = grid.flat().filter((c) => c.count === SENTINEL_COUNT)
    // Jan 1 2025 is Wed → 2 leading days (Mon, Tue 2024-12-30..31)
    // Dec 31 2025 is Wed → 4 trailing days (Thu..Sun 2026-01-01..04)
    // total sentinels = 2 + 4 = 6
    expect(sentinels.length).toBe(6)
  })

  it('sums multiple events on the same day into one cell count', () => {
    // Use mid-day UTC so date stays stable across local timezones.
    const events = [
      evt('task_created',   '2025-05-15T12:00:00Z'),
      evt('task_completed', '2025-05-15T13:00:00Z'),
      evt('decision',       '2025-05-15T14:00:00Z'),
      evt('decision',       '2025-05-15T15:00:00Z'),
    ]
    const grid = buildYearHeatmap(events, 2025)
    const cell = grid.flat().find(
      (c) =>
        c.date.getFullYear() === 2025 &&
        c.date.getMonth() === 4 &&
        c.date.getDate() === 15,
    )
    expect(cell).toBeDefined()
    expect(cell!.count).toBe(4)
    expect(cell!.bucket).toBe(2) // 4 → bucket 2
  })

  it('ignores events outside the requested year', () => {
    const events = [
      evt('task_created', '2024-12-31T12:00:00Z'),
      evt('task_created', '2026-01-01T12:00:00Z'),
    ]
    const grid = buildYearHeatmap(events, 2025)
    const inYearCounts = grid
      .flat()
      .filter((c) => c.count !== SENTINEL_COUNT)
      .map((c) => c.count)
    expect(inYearCounts.every((n) => n === 0)).toBe(true)
  })

  it('first column row 0 is always a Monday (or sentinel)', () => {
    const grid = buildYearHeatmap([], 2025)
    const topLeft = grid[0][0]
    // JS getDay(): Monday = 1
    expect(topLeft.date.getDay()).toBe(1)
  })

  it('last column row 6 is always a Sunday', () => {
    const grid = buildYearHeatmap([], 2025)
    const lastCol = grid[0].length - 1
    const bottomRight = grid[6][lastCol]
    // JS getDay(): Sunday = 0
    expect(bottomRight.date.getDay()).toBe(0)
  })
})
