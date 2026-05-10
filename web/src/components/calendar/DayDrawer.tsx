import { useEffect, useMemo, useRef } from 'react'
import { useTranslation } from 'react-i18next'
import { X } from 'lucide-react'
import type { TimelineEvent, TimelineKind } from '../../types/api'
import { ALL_KINDS, kindColor, kindLabelKey } from './eventStyles'
import { isSameDay } from './dateUtils'

interface DayDrawerProps {
  /** When non-null, the drawer is open for this day. */
  day: Date | null
  events: TimelineEvent[]
  kindFilter: Set<TimelineKind>
  onClose: () => void
}

function fmtTime(iso: string): string {
  return new Date(iso).toLocaleTimeString(undefined, {
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
    hour12: false,
  })
}

function fmtFullDate(d: Date): string {
  return d.toLocaleDateString(undefined, {
    weekday: 'long',
    year: 'numeric',
    month: 'long',
    day: 'numeric',
  })
}

/**
 * DayDrawer slides in from the right and lists events grouped by kind.
 * Closes on backdrop click, ESC, or the close button. Keeps focus
 * trapped inside the drawer while open and restores focus to the
 * triggering element on close (WCAG 2.4.3 Focus Order).
 */
export function DayDrawer({ day, events, kindFilter, onClose }: DayDrawerProps) {
  const { t } = useTranslation()
  const open = day !== null
  const closeBtnRef = useRef<HTMLButtonElement>(null)
  const drawerRef = useRef<HTMLDivElement>(null)

  // ESC closes the drawer.
  useEffect(() => {
    if (!open) return
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        e.stopPropagation()
        onClose()
      }
    }
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  }, [open, onClose])

  // Move focus into drawer on open; restore focus to the trigger on close.
  useEffect(() => {
    if (!open) return
    const triggerElement = document.activeElement as HTMLElement | null
    closeBtnRef.current?.focus()
    return () => {
      triggerElement?.focus?.()
    }
  }, [open])

  // Focus trap: keep Tab cycling within the drawer.
  useEffect(() => {
    if (!open) return
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== 'Tab') return
      const root = drawerRef.current
      if (!root) return
      const focusable = root.querySelectorAll<HTMLElement>(
        'button, [href], input, select, textarea, [tabindex]:not([tabindex="-1"])',
      )
      if (focusable.length === 0) return
      const first = focusable[0]
      const last = focusable[focusable.length - 1]
      if (e.shiftKey && document.activeElement === first) {
        e.preventDefault()
        last.focus()
      } else if (!e.shiftKey && document.activeElement === last) {
        e.preventDefault()
        first.focus()
      }
    }
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  }, [open])

  const dayEvents = useMemo(() => {
    if (!day) return []
    return events
      .filter((e) => kindFilter.has(e.kind) && isSameDay(new Date(e.occurred_at), day))
      .sort((a, b) => a.occurred_at.localeCompare(b.occurred_at))
  }, [day, events, kindFilter])

  const grouped = useMemo(() => {
    const map = new Map<TimelineKind, TimelineEvent[]>()
    for (const ev of dayEvents) {
      let arr = map.get(ev.kind)
      if (!arr) {
        arr = []
        map.set(ev.kind, arr)
      }
      arr.push(ev)
    }
    return map
  }, [dayEvents])

  return (
    <>
      {/* Backdrop */}
      <div
        className="fixed inset-0 z-40 transition-opacity"
        aria-hidden="true"
        style={{
          background: 'rgba(0,0,0,0.4)',
          opacity: open ? 1 : 0,
          pointerEvents: open ? 'auto' : 'none',
        }}
        onClick={onClose}
      />
      {/* Drawer */}
      <aside
        ref={drawerRef}
        role="dialog"
        aria-modal="true"
        aria-label={day ? `Events on ${fmtFullDate(day)}` : 'Day details'}
        className="fixed top-0 right-0 bottom-0 z-50 w-full max-w-md overflow-y-auto transition-transform"
        style={{
          background: 'var(--color-bg-card)',
          borderLeft: '1px solid var(--color-border)',
          transform: open ? 'translateX(0)' : 'translateX(100%)',
          transitionDuration: 'var(--duration-normal)',
        }}
      >
        {day && (
          <>
            <header
              className="sticky top-0 flex items-center justify-between px-4 py-3"
              style={{
                background: 'var(--color-bg-card)',
                borderBottom: '1px solid var(--color-border)',
              }}
            >
              <div>
                <h2 className="text-section" style={{ color: 'var(--color-text-primary)' }}>
                  {fmtFullDate(day)}
                </h2>
                <p className="text-caption" style={{ color: 'var(--color-text-muted)' }}>
                  {t('calendar.eventCount', { count: dayEvents.length })}
                </p>
              </div>
              <button
                ref={closeBtnRef}
                type="button"
                onClick={onClose}
                aria-label="Close day details"
                className="p-2 rounded hover:bg-[var(--color-bg-hover)] focus:outline-none focus:ring-2 focus:ring-[var(--color-border-focus)]"
                style={{ color: 'var(--color-text-primary)' }}
              >
                <X size={18} />
              </button>
            </header>
            <div className="p-4 flex flex-col gap-4">
              {dayEvents.length === 0 && (
                <p className="text-body-sm" style={{ color: 'var(--color-text-muted)' }}>
                  {t('calendar.empty.noEventsOnDay')}
                </p>
              )}
              {ALL_KINDS.map((kind) => {
                const list = grouped.get(kind)
                if (!list || list.length === 0) return null
                const c = kindColor(kind)
                return (
                  <section key={kind}>
                    <h3
                      className="text-label mb-2 flex items-center gap-2"
                      style={{ color: 'var(--color-text-muted)' }}
                    >
                      <span className={`inline-block w-2 h-2 rounded-full ${c.dot}`} aria-hidden="true" />
                      {t(kindLabelKey(kind))} ({list.length})
                    </h3>
                    <ul className="flex flex-col gap-1">
                      {list.map((ev) => (
                        <li
                          key={`${ev.ref_id}-${ev.occurred_at}`}
                          className="flex items-start gap-2 px-2 py-1 rounded"
                          style={{ background: 'var(--color-bg-base)' }}
                        >
                          <span className="text-caption tabular-nums w-[68px] shrink-0 mt-0.5" style={{ color: 'var(--color-text-muted)' }}>
                            {fmtTime(ev.occurred_at)}
                          </span>
                          <span className="text-body-sm flex-1" style={{ color: 'var(--color-text-primary)' }}>
                            {ev.title}
                            {ev.repo_name && (
                              <span className="ml-2 text-caption" style={{ color: 'var(--color-text-muted)' }}>
                                ({ev.repo_name})
                              </span>
                            )}
                          </span>
                        </li>
                      ))}
                    </ul>
                  </section>
                )
              })}
            </div>
          </>
        )}
      </aside>
    </>
  )
}
