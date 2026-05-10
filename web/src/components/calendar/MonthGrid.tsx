import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'
import type { TimelineEvent, TimelineKind } from '../../types/api'
import {
  kindColor,
  kindLabelKey,
  isPlanned,
  eventChipsForDay,
  truncateTitle,
} from './eventStyles'
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
 * MonthGrid renders a 7×6 calendar with up to 3 event chips per cell
 * (Google Calendar style). Each chip shows a colour dot + truncated title;
 * any overflow becomes a "+N more" hint that opens DayDrawer for the full
 * list. Future planning chips (kind=task_due with occurred_at > now)
 * render with a dashed orange outline so they read as "scheduled, not yet
 * done".
 *
 * On narrow screens (<640px / Tailwind sm breakpoint) the layout
 * collapses to dot-only previews — the text would not fit and tapping the
 * cell opens the drawer with full details anyway.
 */
export function MonthGrid({ anchor, events, kindFilter, onDayClick }: MonthGridProps) {
  const { t, i18n } = useTranslation()
  const matrix = useMemo(() => buildMonthMatrix(anchor), [anchor])
  const today = new Date()
  const currentMonth = anchor.getMonth()

  // Locale-aware full-date formatter for cell aria-labels.
  const fullDateFmt = useMemo(
    () => new Intl.DateTimeFormat(i18n.language, { dateStyle: 'full' }),
    [i18n.language],
  )

  // Pre-filter events by kindFilter once; eventChipsForDay then re-filters by
  // day. Doing the kind filter here saves repeated Set.has() calls per cell.
  const filteredEvents = useMemo(
    () => events.filter((ev) => kindFilter.has(ev.kind)),
    [events, kindFilter],
  )

  // Locale-aware short weekday headers, Mon-Sun.
  const weekdays = useMemo(() => {
    const fmt = new Intl.DateTimeFormat(i18n.language, { weekday: 'short' })
    // 2024-01-01 is a Monday, anchor weekday formatting on it.
    return Array.from({ length: 7 }, (_, i) =>
      fmt.format(new Date(2024, 0, 1 + i)),
    )
  }, [i18n.language])

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
          const key = dateKey(day)
          const { shown, moreCount } = eventChipsForDay(filteredEvents, key, dateKey, 3)
          // Distinct kind list for the mobile dot summary.
          const dotKinds = Array.from(new Set(shown.map((ev) => ev.kind))).slice(0, 4)

          return (
            <button
              key={day.toISOString()}
              type="button"
              onClick={() => onDayClick(day)}
              className="flex flex-col items-stretch p-1.5 sm:p-2 min-h-[80px] sm:min-h-[110px] text-left transition-colors cursor-pointer focus:outline-none focus:ring-2 focus:ring-inset"
              style={{
                background: 'var(--color-bg-card)',
                color: inMonth
                  ? 'var(--color-text-primary)'
                  : 'var(--color-text-disabled)',
                outline: isToday ? '2px solid var(--color-accent-blue)' : 'none',
                outlineOffset: '-2px',
              }}
              aria-label={fullDateFmt.format(day)}
              data-testid="calendar-day-cell"
              data-day={key}
            >
              <span className="text-body-sm font-medium mb-1">{day.getDate()}</span>

              {/* Mobile: dots-only preview (one per distinct kind). */}
              <div
                className="flex flex-wrap gap-1 mt-auto pt-1 sm:hidden"
                aria-hidden="true"
                data-testid="calendar-day-dots"
              >
                {dotKinds.map((k) => (
                  <span
                    key={k}
                    className={`inline-block w-2 h-2 rounded-full ${kindColor(k).dot}`}
                  />
                ))}
                {moreCount > 0 && (
                  <span
                    className="text-caption"
                    style={{ color: 'var(--color-text-muted)' }}
                  >
                    +{moreCount}
                  </span>
                )}
              </div>

              {/* Desktop: text chips up to 3 per cell + +N more hint. */}
              <div className="hidden sm:flex sm:flex-col sm:gap-0.5">
                {shown.map((ev) => {
                  const c = kindColor(ev.kind)
                  const planned = isPlanned(ev, today)
                  const kindLabel = t(kindLabelKey(ev.kind))
                  const truncated = truncateTitle(ev.title, 20)
                  return (
                    <span
                      key={`${ev.kind}-${ev.ref_id}-${ev.occurred_at}`}
                      className={`flex items-center gap-1 px-1 py-0.5 rounded text-caption truncate ${planned ? 'border border-dashed' : ''}`}
                      style={{
                        background: planned ? 'transparent' : 'var(--color-bg-base)',
                        borderColor: planned ? 'var(--color-accent-orange, #f97316)' : undefined,
                        color: 'var(--color-text-primary)',
                      }}
                      role="note"
                      aria-label={t('calendar.chipAriaLabel', { kind: kindLabel, title: ev.title })}
                      title={ev.title}
                      data-testid="calendar-event-chip"
                      data-planned={planned ? 'true' : 'false'}
                    >
                      <span
                        className={`inline-block w-2 h-2 rounded-full shrink-0 ${c.dot}`}
                        aria-hidden="true"
                      />
                      <span className="truncate">{truncated}</span>
                    </span>
                  )
                })}
                {moreCount > 0 && (
                  <span
                    className="text-caption px-1"
                    style={{ color: 'var(--color-text-muted)' }}
                    aria-label={t('calendar.moreButtonAriaLabel', { count: moreCount })}
                    data-testid="calendar-more-button"
                  >
                    {t('calendar.moreEvents', { count: moreCount })}
                  </span>
                )}
              </div>
            </button>
          )
        })}
      </div>
    </div>
  )
}
