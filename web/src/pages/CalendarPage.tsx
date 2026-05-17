import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useTimeline } from '../hooks/useTimeline'
import { CalendarControls, type CalendarView } from '../components/calendar/CalendarControls'
import { MonthGrid } from '../components/calendar/MonthGrid'
import { WeekList } from '../components/calendar/WeekList'
import { YearHeatmap } from '../components/calendar/YearHeatmap'
import { DayDrawer } from '../components/calendar/DayDrawer'
import { ALL_KINDS } from '../components/calendar/eventStyles'
import { buildMonthMatrix, mondayOf } from '../components/calendar/dateUtils'
import { LoadingSkeleton } from '../components/ui/LoadingSkeleton'
import type { TimelineEvent, TimelineKind } from '../types/api'

/**
 * CalendarPage renders the maintainer's personal OS activity over time.
 * Three layouts (month grid / week list / year heatmap) share the same
 * `useTimeline` fetch, kind filter, and day drawer.
 */
export function CalendarPage() {
  const { t } = useTranslation()
  const [selectedDate, setSelectedDate] = useState<Date>(() => new Date())
  const [view, setView] = useState<CalendarView>('month')
  const [kindFilter, setKindFilter] = useState<Set<TimelineKind>>(() => new Set(ALL_KINDS))
  const [drawerDate, setDrawerDate] = useState<Date | null>(null)
  const [repoFilter, setRepoFilter] = useState<string | null>(null)

  // Compute the [from, to] range to fetch based on the active view.
  const [from, to] = useMemo<[Date, Date]>(() => {
    if (view === 'month') {
      const matrix = buildMonthMatrix(selectedDate)
      const first = matrix[0][0]
      const last = matrix[matrix.length - 1][6]
      const start = new Date(first.getFullYear(), first.getMonth(), first.getDate(), 0, 0, 0)
      const end = new Date(last.getFullYear(), last.getMonth(), last.getDate(), 23, 59, 59)
      return [start, end]
    }
    if (view === 'year') {
      const year = selectedDate.getFullYear()
      const start = new Date(year, 0, 1, 0, 0, 0)
      const end = new Date(year, 11, 31, 23, 59, 59)
      return [start, end]
    }
    // week view
    const monday = mondayOf(selectedDate)
    const sunday = new Date(monday.getFullYear(), monday.getMonth(), monday.getDate() + 6, 23, 59, 59)
    const start = new Date(monday.getFullYear(), monday.getMonth(), monday.getDate(), 0, 0, 0)
    return [start, sunday]
  }, [view, selectedDate])

  const { data, isLoading, isError } = useTimeline(from, to)
  const events = useMemo(() => data?.events ?? [], [data])

  // Collect sorted unique repo names from the fetched events for the filter dropdown.
  const repoNames = useMemo<string[]>(() => {
    const names = new Set<string>()
    for (const ev of events) {
      if (ev.repo_name) names.add(ev.repo_name)
    }
    return Array.from(names).sort()
  }, [events])

  // Apply repo filter — null means "show all repos".
  const filteredEvents = useMemo<TimelineEvent[]>(() => {
    if (repoFilter === null) return events
    return events.filter((ev) => ev.repo_name === repoFilter)
  }, [events, repoFilter])

  const toggleKind = (k: TimelineKind) => {
    setKindFilter((prev) => {
      const next = new Set(prev)
      if (next.has(k)) {
        next.delete(k)
      } else {
        next.add(k)
      }
      return next
    })
  }

  return (
    <div className="max-w-[1200px] mx-auto pb-10">
      {/* Repo / project filter bar */}
      {repoNames.length > 0 && (
        <div
          className="flex items-center gap-2 px-4 py-2 border-b"
          style={{
            background: 'var(--color-bg-base)',
            borderColor: 'var(--color-border)',
          }}
        >
          <label
            htmlFor="calendar-repo-filter"
            className="text-label shrink-0"
            style={{ color: 'var(--color-text-muted)' }}
          >
            {t('calendar.controls.repoFilter')}
          </label>
          <select
            id="calendar-repo-filter"
            value={repoFilter ?? ''}
            onChange={(e) => setRepoFilter(e.target.value === '' ? null : e.target.value)}
            className="px-2 py-1 rounded text-body-sm"
            style={{
              background: 'var(--color-bg-input)',
              border: '1px solid var(--color-border)',
              color: 'var(--color-text-primary)',
            }}
            aria-label="Filter events by repo"
          >
            <option value="">{t('calendar.controls.allRepos')}</option>
            {repoNames.map((name) => (
              <option key={name} value={name}>
                {name}
              </option>
            ))}
          </select>
          {repoFilter !== null && (
            <button
              type="button"
              className="text-body-sm px-2 py-1 rounded"
              style={{
                color: 'var(--color-accent-blue)',
                background: 'transparent',
                border: 'none',
                cursor: 'pointer',
              }}
              onClick={() => setRepoFilter(null)}
              aria-label="Clear repo filter"
            >
              {t('calendar.controls.clearFilter')}
            </button>
          )}
        </div>
      )}
      <CalendarControls
        selectedDate={selectedDate}
        view={view}
        kindFilter={kindFilter}
        onDateChange={setSelectedDate}
        onViewChange={setView}
        onToggleKind={toggleKind}
      />

      <div className="px-4">
        {isError && (
          <div
            className="rounded-md p-3 mb-4 text-body-sm"
            style={{
              background: '#2e0a0a',
              border: '1px solid var(--color-error)',
              color: 'var(--color-error)',
            }}
          >
            {t('calendar.errors.loadFailed')}
          </div>
        )}

        {isLoading ? (
          <LoadingSkeleton className="w-full" style={{ height: '480px' }} />
        ) : view === 'month' ? (
          <MonthGrid
            anchor={selectedDate}
            events={filteredEvents}
            kindFilter={kindFilter}
            onDayClick={(d) => setDrawerDate(d)}
          />
        ) : view === 'year' ? (
          <YearHeatmap
            year={selectedDate.getFullYear()}
            events={filteredEvents}
            onMonthJump={(m) => {
              setView('month')
              setSelectedDate(m)
            }}
          />
        ) : (
          <WeekList
            anchor={selectedDate}
            events={filteredEvents}
            kindFilter={kindFilter}
            onEventClick={(d) => setDrawerDate(d)}
          />
        )}
      </div>

      <DayDrawer
        day={drawerDate}
        events={filteredEvents}
        kindFilter={kindFilter}
        onClose={() => setDrawerDate(null)}
      />
    </div>
  )
}
