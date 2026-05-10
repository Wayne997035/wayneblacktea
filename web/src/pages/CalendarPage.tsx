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
import type { TimelineKind } from '../types/api'

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
  const events = data?.events ?? []

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
            events={events}
            kindFilter={kindFilter}
            onDayClick={(d) => setDrawerDate(d)}
          />
        ) : view === 'year' ? (
          <YearHeatmap
            year={selectedDate.getFullYear()}
            events={events}
            onMonthJump={(m) => {
              setView('month')
              setSelectedDate(m)
            }}
          />
        ) : (
          <WeekList
            anchor={selectedDate}
            events={events}
            kindFilter={kindFilter}
            onEventClick={(d) => setDrawerDate(d)}
          />
        )}
      </div>

      <DayDrawer
        day={drawerDate}
        events={events}
        kindFilter={kindFilter}
        onClose={() => setDrawerDate(null)}
      />
    </div>
  )
}
