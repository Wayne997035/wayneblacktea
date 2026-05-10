import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'
import type { TimelineEvent, TimelineKind } from '../../types/api'
import { kindColor, kindLabelKey } from './eventStyles'
import { mondayOf, dateKey } from './dateUtils'

interface WeekListProps {
  /** Date inside the week to render (week is Mon..Sun). */
  anchor: Date
  events: TimelineEvent[]
  kindFilter: Set<TimelineKind>
  onEventClick: (day: Date, ev: TimelineEvent) => void
}

function fmtTime(iso: string): string {
  const d = new Date(iso)
  return d.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false })
}

function fmtDate(d: Date): string {
  return d.toLocaleDateString(undefined, { weekday: 'short', month: 'short', day: 'numeric' })
}

/**
 * WeekList renders one section per day Mon..Sun. Events inside each day
 * are listed in chronological order with HH:mm:ss + colored kind dot +
 * title. Empty days still get a header so the user can see the gap.
 */
export function WeekList({ anchor, events, kindFilter, onEventClick }: WeekListProps) {
  const { t } = useTranslation()
  const monday = useMemo(() => mondayOf(anchor), [anchor])

  const days = useMemo(() => {
    return Array.from({ length: 7 }, (_, i) => {
      const d = new Date(monday.getFullYear(), monday.getMonth(), monday.getDate() + i)
      return d
    })
  }, [monday])

  // Group events by day key, sorted ascending by occurred_at.
  const grouped = useMemo(() => {
    const map = new Map<string, TimelineEvent[]>()
    for (const ev of events) {
      if (!kindFilter.has(ev.kind)) continue
      const key = dateKey(new Date(ev.occurred_at))
      let arr = map.get(key)
      if (!arr) {
        arr = []
        map.set(key, arr)
      }
      arr.push(ev)
    }
    for (const arr of map.values()) {
      arr.sort((a, b) => a.occurred_at.localeCompare(b.occurred_at))
    }
    return map
  }, [events, kindFilter])

  return (
    <div className="flex flex-col gap-3" data-testid="calendar-week-list">
      {days.map((day) => {
        const key = dateKey(day)
        const dayEvents = grouped.get(key) ?? []
        return (
          <section
            key={key}
            className="rounded-md p-3"
            style={{ background: 'var(--color-bg-card)', border: '1px solid var(--color-border)' }}
            data-testid="calendar-week-day"
            data-day={key}
          >
            <header className="flex items-baseline justify-between mb-2">
              <h3 className="text-card-title" style={{ color: 'var(--color-text-primary)' }}>
                {fmtDate(day)}
              </h3>
              <span className="text-caption" style={{ color: 'var(--color-text-muted)' }}>
                {dayEvents.length}
              </span>
            </header>
            {dayEvents.length === 0 ? (
              <p className="text-body-sm" style={{ color: 'var(--color-text-muted)' }}>
                {t('calendar.empty.noEvents')}
              </p>
            ) : (
              <ul className="flex flex-col gap-1">
                {dayEvents.map((ev) => {
                  const c = kindColor(ev.kind)
                  return (
                    <li key={`${ev.kind}-${ev.ref_id}-${ev.occurred_at}`}>
                      <button
                        type="button"
                        onClick={() => onEventClick(day, ev)}
                        className="w-full flex items-center gap-2 px-2 py-1 rounded text-left hover:bg-[var(--color-bg-hover)] focus:outline-none focus:ring-2 focus:ring-[var(--color-border-focus)]"
                        aria-label={`${t(kindLabelKey(ev.kind))} ${fmtTime(ev.occurred_at)} ${ev.title}`}
                      >
                        <span className="text-caption tabular-nums w-[68px] shrink-0" style={{ color: 'var(--color-text-muted)' }}>
                          {fmtTime(ev.occurred_at)}
                        </span>
                        <span className={`inline-block w-2 h-2 rounded-full shrink-0 ${c.dot}`} aria-hidden="true" />
                        <span className="text-body-sm truncate" style={{ color: 'var(--color-text-primary)' }}>
                          {ev.title}
                        </span>
                        {ev.repo_name && (
                          <span className="text-caption ml-auto shrink-0" style={{ color: 'var(--color-text-muted)' }}>
                            {ev.repo_name}
                          </span>
                        )}
                      </button>
                    </li>
                  )
                })}
              </ul>
            )}
          </section>
        )
      })}
    </div>
  )
}
