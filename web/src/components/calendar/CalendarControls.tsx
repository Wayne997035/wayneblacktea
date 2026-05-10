import { useTranslation } from 'react-i18next'
import type { TimelineKind } from '../../types/api'
import { ALL_KINDS, kindColor, kindLabelKey } from './eventStyles'

export type CalendarView = 'month' | 'week'

interface CalendarControlsProps {
  selectedDate: Date
  view: CalendarView
  kindFilter: Set<TimelineKind>
  onDateChange: (d: Date) => void
  onViewChange: (v: CalendarView) => void
  onToggleKind: (k: TimelineKind) => void
}

/** dateInputValue formats a Date into the YYYY-MM-DD string an `<input type="date">` expects. */
function dateInputValue(d: Date): string {
  const y = d.getFullYear()
  const m = String(d.getMonth() + 1).padStart(2, '0')
  const day = String(d.getDate()).padStart(2, '0')
  return `${y}-${m}-${day}`
}

/**
 * CalendarControls: sticky top bar with date picker, view toggle, and
 * kind filter checkboxes.
 */
export function CalendarControls({
  selectedDate,
  view,
  kindFilter,
  onDateChange,
  onViewChange,
  onToggleKind,
}: CalendarControlsProps) {
  const { t } = useTranslation()
  return (
    <div
      className="sticky top-0 z-10 flex flex-wrap items-center gap-3 px-4 py-3 mb-4"
      style={{
        background: 'var(--color-bg-base)',
        borderBottom: '1px solid var(--color-border)',
      }}
    >
      {/* Date picker */}
      <label className="flex items-center gap-2">
        <span className="text-label" style={{ color: 'var(--color-text-muted)' }}>
          {t('calendar.controls.date')}
        </span>
        <input
          type="date"
          value={dateInputValue(selectedDate)}
          onChange={(e) => {
            const [y, m, d] = e.target.value.split('-').map(Number)
            if (!y || !m || !d) return
            onDateChange(new Date(y, m - 1, d))
          }}
          className="px-2 py-1 rounded text-body-sm"
          style={{
            background: 'var(--color-bg-input)',
            border: '1px solid var(--color-border)',
            color: 'var(--color-text-primary)',
          }}
          aria-label="Jump to date"
        />
      </label>

      {/* View toggle */}
      <div role="group" aria-label="View toggle" className="flex rounded overflow-hidden">
        {(['month', 'week'] as CalendarView[]).map((v) => {
          const active = v === view
          return (
            <button
              key={v}
              type="button"
              onClick={() => onViewChange(v)}
              aria-pressed={active}
              className="px-3 py-1 text-body-sm transition-colors focus:outline-none focus:ring-2 focus:ring-[var(--color-border-focus)]"
              style={{
                background: active ? 'var(--color-accent-blue)' : 'var(--color-bg-input)',
                color: active ? 'var(--color-bg-base)' : 'var(--color-text-primary)',
                border: '1px solid var(--color-border)',
                borderLeftWidth: v === 'week' ? 0 : 1,
              }}
            >
              {v === 'month' ? t('calendar.controls.viewMonth') : t('calendar.controls.viewWeek')}
            </button>
          )
        })}
      </div>

      {/* Kind filter */}
      <div className="flex flex-wrap items-center gap-2 ml-auto">
        {ALL_KINDS.map((k) => {
          const checked = kindFilter.has(k)
          const c = kindColor(k)
          const label = t(kindLabelKey(k))
          return (
            <label
              key={k}
              className="flex items-center gap-1 cursor-pointer text-body-sm"
              style={{ color: 'var(--color-text-primary)' }}
            >
              <input
                type="checkbox"
                checked={checked}
                onChange={() => onToggleKind(k)}
                className="accent-current"
                aria-label={`Toggle ${label}`}
              />
              <span className={`inline-block w-2 h-2 rounded-full ${c.dot}`} aria-hidden="true" />
              <span style={{ color: checked ? 'var(--color-text-primary)' : 'var(--color-text-muted)' }}>
                {label}
              </span>
            </label>
          )
        })}
      </div>
    </div>
  )
}
