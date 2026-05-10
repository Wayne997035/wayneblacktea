import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'
import type { TimelineEvent } from '../../types/api'
import { buildYearHeatmap, SENTINEL_COUNT, type HeatmapCell } from './heatmapData'
import { dateKey } from './dateUtils'

interface YearHeatmapProps {
  /** Calendar year (e.g. 2025) to render. */
  year: number
  events: TimelineEvent[]
  /**
   * Called with the first day of the clicked cell's month, so the
   * parent can switch back to the month view focused on that month.
   */
  onMonthJump: (month: Date) => void
}

/**
 * Bucket → CSS background colour. Bucket 0 uses a muted card-bg token
 * so empty cells stay visible against the page; buckets 1..4 walk a
 * green ramp. Inline styles keep this independent of Tailwind class
 * generation (the v4 JIT cannot statically see indexed access).
 */
const BUCKET_BG: Readonly<Record<HeatmapCell['bucket'], string>> = {
  0: 'var(--color-bg-card)',
  1: '#0e4429',
  2: '#006d32',
  3: '#26a641',
  4: '#39d353',
}

const CELL = 12
const GAP = 2
const STEP = CELL + GAP
const ROWS = 7

/**
 * YearHeatmap renders a GitHub-style 7×~53 contribution grid as a
 * single SVG. Each cell is an interactive `<rect>` with an SVG
 * `<title>` tooltip and an `aria-label` for screen readers. Click
 * jumps the parent view to the month containing that cell.
 */
export function YearHeatmap({ year, events, onMonthJump }: YearHeatmapProps) {
  const { t, i18n } = useTranslation()

  const grid = useMemo(() => buildYearHeatmap(events, year), [events, year])
  const numWeeks = grid[0]?.length ?? 0

  // Locale-aware formatters (memoised so they don't recreate per cell).
  const fullDateFmt = useMemo(
    () => new Intl.DateTimeFormat(i18n.language, { dateStyle: 'full' }),
    [i18n.language],
  )
  const monthShortFmt = useMemo(
    () => new Intl.DateTimeFormat(i18n.language, { month: 'short' }),
    [i18n.language],
  )
  const weekdayShortFmt = useMemo(
    () => new Intl.DateTimeFormat(i18n.language, { weekday: 'short' }),
    [i18n.language],
  )

  // Weekday labels Mon..Sun (3 visible: Mon=0, Wed=2, Fri=4).
  const weekdayLabels = useMemo(() => {
    // 2024-01-01 is a Monday; format that anchor + offset for stable labels.
    return Array.from({ length: 7 }, (_, i) =>
      weekdayShortFmt.format(new Date(2024, 0, 1 + i)),
    )
  }, [weekdayShortFmt])

  // Month labels: first column whose top cell is the first Monday of the
  // month gets a label sitting above it. Skips a label if it would
  // overlap the previous one (≥ 3 columns apart) for readability.
  const monthLabels = useMemo(() => {
    const labels: { col: number; text: string }[] = []
    let lastMonth = -1
    let lastCol = -3
    for (let col = 0; col < numWeeks; col++) {
      // Use any in-year cell in the column; prefer the top row.
      let monthForCol = -1
      let dateForCol: Date | null = null
      for (let row = 0; row < ROWS; row++) {
        const cell = grid[row][col]
        if (cell.count === SENTINEL_COUNT) continue
        if (cell.date.getFullYear() === year) {
          monthForCol = cell.date.getMonth()
          dateForCol = cell.date
          break
        }
      }
      if (monthForCol === -1 || !dateForCol) continue
      if (monthForCol !== lastMonth && col - lastCol >= 3) {
        labels.push({ col, text: monthShortFmt.format(dateForCol) })
        lastMonth = monthForCol
        lastCol = col
      }
    }
    return labels
  }, [grid, monthShortFmt, numWeeks, year])

  const labelGutter = 28 // left gutter for weekday labels (px)
  const topGutter = 16 // top gutter for month labels (px)
  const width = labelGutter + numWeeks * STEP
  const height = topGutter + ROWS * STEP

  return (
    <div className="w-full overflow-x-auto">
      <svg
        role="grid"
        aria-label={t('calendar.heatmap.gridLabel', { year })}
        width={width}
        height={height}
        viewBox={`0 0 ${width} ${height}`}
        style={{ display: 'block' }}
      >
        {/* Month labels */}
        <g aria-hidden="true">
          {monthLabels.map(({ col, text }) => (
            <text
              key={`m-${col}`}
              x={labelGutter + col * STEP}
              y={11}
              fontSize={10}
              fill="var(--color-text-muted)"
            >
              {text}
            </text>
          ))}
        </g>

        {/* Weekday labels — Mon (row 0), Wed (row 2), Fri (row 4) visible */}
        <g aria-hidden="true">
          {[0, 2, 4].map((row) => (
            <text
              key={`w-${row}`}
              x={0}
              y={topGutter + row * STEP + CELL - 2}
              fontSize={10}
              fill="var(--color-text-muted)"
            >
              {weekdayLabels[row]}
            </text>
          ))}
        </g>

        {/* Cells */}
        <g>
          {grid.map((row, rowIdx) =>
            row.map((cell, colIdx) => {
              const isSentinel = cell.count === SENTINEL_COUNT
              if (isSentinel) {
                // Render an invisible placeholder so layout math stays trivial.
                return (
                  <rect
                    key={`s-${rowIdx}-${colIdx}`}
                    x={labelGutter + colIdx * STEP}
                    y={topGutter + rowIdx * STEP}
                    width={CELL}
                    height={CELL}
                    fill="transparent"
                    aria-hidden="true"
                  />
                )
              }
              const date = cell.date
              const dateLabel = fullDateFmt.format(date)
              const countLabel = t('calendar.eventCount', { count: cell.count })
              const tooltip = `${dateLabel} — ${countLabel}`
              return (
                <rect
                  key={`c-${dateKey(date)}`}
                  x={labelGutter + colIdx * STEP}
                  y={topGutter + rowIdx * STEP}
                  width={CELL}
                  height={CELL}
                  rx={2}
                  ry={2}
                  fill={BUCKET_BG[cell.bucket]}
                  stroke="var(--color-border)"
                  strokeWidth={cell.bucket === 0 ? 1 : 0}
                  className="cursor-pointer focus:outline-none"
                  tabIndex={0}
                  role="gridcell"
                  aria-label={tooltip}
                  data-testid="heatmap-cell"
                  data-day={dateKey(date)}
                  data-bucket={cell.bucket}
                  onClick={() => onMonthJump(new Date(date.getFullYear(), date.getMonth(), 1))}
                  onKeyDown={(e) => {
                    if (e.key === 'Enter' || e.key === ' ') {
                      e.preventDefault()
                      onMonthJump(new Date(date.getFullYear(), date.getMonth(), 1))
                    }
                  }}
                >
                  <title>{tooltip}</title>
                </rect>
              )
            }),
          )}
        </g>
      </svg>
    </div>
  )
}
