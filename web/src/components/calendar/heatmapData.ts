import type { TimelineEvent } from '../../types/api'
import { dateKey } from './dateUtils'

/**
 * HeatmapCell represents one day in the year heatmap grid. `count = -1`
 * marks a "sentinel" cell that falls outside the requested year and
 * should be rendered blank by the consumer.
 */
export interface HeatmapCell {
  date: Date
  count: number
  bucket: 0 | 1 | 2 | 3 | 4
}

/**
 * bucketForCount maps an event count onto a 5-step intensity bucket
 * using a log-scale so that "11 events" and "100 events" don't both
 * saturate the same colour. The boundaries match the spec:
 *   0       → bucket 0
 *   1..2    → bucket 1
 *   3..5    → bucket 2
 *   6..10   → bucket 3
 *   ≥ 11    → bucket 4
 *
 * Implemented via Math.log1p so the curve is smooth and the function
 * stays pure / easy to property-test.
 */
export function bucketForCount(n: number): 0 | 1 | 2 | 3 | 4 {
  if (n <= 0) return 0
  if (n <= 2) return 1
  if (n <= 5) return 2
  if (n <= 10) return 3
  return 4
}

/** Sentinel cell value used for days that fall outside the requested year. */
export const SENTINEL_COUNT = -1

/**
 * buildYearHeatmap aggregates events by local-day and returns a
 * Mon..Sun × ~53-week grid covering the entire `year`. The first
 * column is the ISO week containing Jan 1 (so leading days from the
 * previous December may appear in column 0); the last column is the
 * ISO week containing Dec 31 (trailing days from next January may
 * appear in the final column). Out-of-year cells use SENTINEL_COUNT
 * so the renderer can blank them out without losing grid alignment.
 *
 * Returned shape: 7 rows (Mon=0 .. Sun=6) × N columns (52 or 53).
 */
export function buildYearHeatmap(events: TimelineEvent[], year: number): HeatmapCell[][] {
  // 1) bucket events by local YYYY-MM-DD
  const counts = new Map<string, number>()
  for (const ev of events) {
    const d = new Date(ev.occurred_at)
    if (d.getFullYear() !== year) continue
    const k = dateKey(d)
    counts.set(k, (counts.get(k) ?? 0) + 1)
  }

  // 2) determine the Monday on/before Jan 1 (column 0) and the Sunday on/after Dec 31 (last column)
  const jan1 = new Date(year, 0, 1)
  const dec31 = new Date(year, 11, 31)
  const isoWeekdayJan1 = (jan1.getDay() + 6) % 7
  const isoWeekdayDec31 = (dec31.getDay() + 6) % 7
  const start = new Date(year, 0, 1 - isoWeekdayJan1)
  const end = new Date(year, 11, 31 + (6 - isoWeekdayDec31))

  // 3) compute number of full weeks
  const msPerDay = 24 * 60 * 60 * 1000
  const totalDays = Math.round((end.getTime() - start.getTime()) / msPerDay) + 1
  const numWeeks = totalDays / 7

  const rows: HeatmapCell[][] = Array.from({ length: 7 }, () => [])
  for (let col = 0; col < numWeeks; col++) {
    for (let row = 0; row < 7; row++) {
      const d = new Date(start.getFullYear(), start.getMonth(), start.getDate() + col * 7 + row)
      const inYear = d.getFullYear() === year
      const count = inYear ? counts.get(dateKey(d)) ?? 0 : SENTINEL_COUNT
      const bucket = inYear ? bucketForCount(count) : 0
      rows[row].push({ date: d, count, bucket })
    }
  }
  return rows
}
