import { useMemo } from 'react'
import type { TimelineEvent, TimelineKind } from '../../types/api'
import { kindColor, kindLabel } from './eventStyles'
import { buildMonthMatrix, dateKey, isSameDay } from './dateUtils'

interface MonthGridProps {
  /** Date inside the month to render. */
  anchor: Date
  events: TimelineEvent[]
  /** Set of allowed kinds; events outside the set are ignored. */
  kindFilter: Set<TimelineKind>
  onDayClick: (day: Date) => void
}

/**
 * MonthGrid renders a 7×6 calendar with up to 4 colored kind dots per
 * cell (one per distinct kind on that day). Cells outside the current
 * month are dimmed; today gets a ring border.
 */
export function MonthGrid({ anchor, events, kindFilter, onDayClick }: MonthGridProps) {
  const matrix = useMemo(() => buildMonthMatrix(anchor), [anchor])
  const today = new Date()
  const currentMonth = anchor.getMonth()

  // Group events by day → set of unique kinds
  const dayKinds = useMemo(() => {
    const map = new Map<string, Set<TimelineKind>>()
    for (const ev of events) {
      if (!kindFilter.has(ev.kind)) continue
      const d = new Date(ev.occurred_at)
      const key = dateKey(d)
      let set = map.get(key)
      if (!set) {
        set = new Set<TimelineKind>()
        map.set(key, set)
      }
      set.add(ev.kind)
    }
    return map
  }, [events, kindFilter])

  // Locale-aware short weekday headers, Mon-Sun.
  const weekdays = useMemo(() => {
    const fmt = new Intl.DateTimeFormat(undefined, { weekday: 'short' })
    // 2024-01-01 is a Monday, anchor weekday formatting on it.
    return Array.from({ length: 7 }, (_, i) =>
      fmt.format(new Date(2024, 0, 1 + i)),
    )
  }, [])

  return (
    <div className="w-full" role="grid" aria-label="Month grid">
      <div className="grid grid-cols-7 gap-px mb-1">
        {weekdays.map((w) => (
          <div
            key={w}
            className="text-label py-2 text-center"
            style={{ color: 'var(--color-text-muted)' }}
          >
            {w}
          </div>
        ))}
      </div>
      <div
        className="grid grid-cols-7 gap-px"
        style={{ background: 'var(--color-border)' }}
      >
        {matrix.flat().map((day) => {
          const inMonth = day.getMonth() === currentMonth
          const isToday = isSameDay(day, today)
          const kinds = dayKinds.get(dateKey(day))
          const kindList = kinds ? Array.from(kinds) : []
          const visible = kindList.slice(0, 4)
          const overflow = Math.max(0, kindList.length - visible.length)

          return (
            <button
              key={day.toISOString()}
              type="button"
              onClick={() => onDayClick(day)}
              className="flex flex-col items-start p-2 min-h-[80px] text-left transition-colors cursor-pointer focus:outline-none focus:ring-2 focus:ring-inset"
              style={{
                background: 'var(--color-bg-card)',
                color: inMonth
                  ? 'var(--color-text-primary)'
                  : 'var(--color-text-disabled)',
                outline: isToday ? '2px solid var(--color-accent-blue)' : 'none',
                outlineOffset: '-2px',
              }}
              aria-label={day.toDateString()}
              data-testid="calendar-day-cell"
              data-day={dateKey(day)}
            >
              <span className="text-body-sm font-medium">{day.getDate()}</span>
              {visible.length > 0 && (
                <div className="flex flex-wrap gap-1 mt-auto pt-1" aria-hidden="true">
                  {visible.map((k) => (
                    <span
                      key={k}
                      title={kindLabel(k)}
                      className={`inline-block w-2 h-2 rounded-full ${kindColor(k).dot}`}
                    />
                  ))}
                  {overflow > 0 && (
                    <span
                      className="text-caption"
                      style={{ color: 'var(--color-text-muted)' }}
                    >
                      +{overflow}
                    </span>
                  )}
                </div>
              )}
            </button>
          )
        })}
      </div>
    </div>
  )
}
