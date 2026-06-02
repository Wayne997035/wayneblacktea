import type { TimelineEvent, TimelineKind } from '../../types/api'

/**
 * KIND_STYLES maps each kind to a colour family used both as Tailwind
 * background ("dot" colour) and a darker text variant for badges.
 * task_due uses orange to distinguish forward-looking planning events from
 * completed/historical green/gray.
 */
const KIND_STYLES: Record<TimelineKind, { dot: string; text: string; ring: string }> = {
  task_created:    { dot: 'bg-green-500',  text: 'text-green-700',  ring: 'ring-green-500' },
  task_completed:  { dot: 'bg-green-500',  text: 'text-green-700',  ring: 'ring-green-500' },
  task_due:        { dot: 'bg-orange-500', text: 'text-orange-700', ring: 'ring-orange-500' },
  decision:        { dot: 'bg-yellow-500', text: 'text-yellow-700', ring: 'ring-yellow-500' },
  knowledge:       { dot: 'bg-blue-500',   text: 'text-blue-700',   ring: 'ring-blue-500' },
  concept:         { dot: 'bg-blue-500',   text: 'text-blue-700',   ring: 'ring-blue-500' },
  review_submitted:{ dot: 'bg-purple-500', text: 'text-purple-700', ring: 'ring-purple-500' },
  activity:        { dot: 'bg-gray-500',   text: 'text-gray-700',   ring: 'ring-gray-500' },
  handoff_created: { dot: 'bg-gray-500',   text: 'text-gray-700',   ring: 'ring-gray-500' },
  handoff_resolved:{ dot: 'bg-gray-500',   text: 'text-gray-700',   ring: 'ring-gray-500' },
}

const FALLBACK = { dot: 'bg-gray-400', text: 'text-gray-600', ring: 'ring-gray-400' }

/**
 * kindColor returns the Tailwind classes for a given timeline kind.
 * Unknown values fall back to a neutral gray palette so the UI never
 * crashes if the backend introduces a new kind ahead of the frontend.
 */
export function kindColor(kind: TimelineKind | string): { dot: string; text: string; ring: string } {
  return KIND_STYLES[kind as TimelineKind] ?? FALLBACK
}

/**
 * KIND_LABEL_KEYS maps each timeline kind to its i18n key under the
 * `calendar.kinds.*` namespace. Use `kindLabelKey()` from non-React
 * modules and let React callers translate via `t(kindLabelKey(kind))`.
 * Unknown kinds fall back to the raw kind string so the UI never
 * displays an empty label.
 */
const KIND_LABEL_KEYS: Record<TimelineKind, string> = {
  task_created:     'calendar.kinds.task_created',
  task_completed:   'calendar.kinds.task_completed',
  task_due:         'calendar.kinds.task_due',
  decision:         'calendar.kinds.decision',
  activity:         'calendar.kinds.activity',
  knowledge:        'calendar.kinds.knowledge',
  concept:          'calendar.kinds.concept',
  review_submitted: 'calendar.kinds.review_submitted',
  handoff_created:  'calendar.kinds.handoff_created',
  handoff_resolved: 'calendar.kinds.handoff_resolved',
}

/**
 * kindLabelKey returns the i18n translation key for a timeline kind.
 * Callers in React land should pass the result through `t()` to render.
 * Unknown kinds return the raw kind string (which `t()` will then
 * leave untranslated, matching the previous fallback behaviour).
 */
export function kindLabelKey(kind: TimelineKind | string): string {
  return KIND_LABEL_KEYS[kind as TimelineKind] ?? String(kind)
}

export const ALL_KINDS: TimelineKind[] = [
  'task_created',
  'task_completed',
  'task_due',
  'decision',
  'activity',
  'knowledge',
  'concept',
  'review_submitted',
  'handoff_created',
  'handoff_resolved',
]

/**
 * DEFAULT_KINDS is the set of event kinds shown by default on the calendar.
 * 'activity' is excluded from the default because the backend can emit up to
 * 10 000 activity events per month, flooding the per-cell chip view with
 * "+N more" overflow. Users can still re-enable it via CalendarControls.
 */
export const DEFAULT_KINDS: TimelineKind[] = ALL_KINDS.filter((k) => k !== 'activity')

/**
 * isPlanned reports whether an event represents a future-scheduled item that
 * has not happened yet. Used to render outlined / dashed chips so the user
 * can distinguish "what's coming up" from "what has occurred".
 *
 * Heuristic: kind === 'task_due' AND occurred_at > now. We don't outline
 * task_due that already passed (overdue) — those should still pop visually
 * but as solid orange chips signalling "this was due, no action recorded".
 *
 * The `now` argument is injectable for deterministic testing; defaults to
 * Date.now().
 */
export function isPlanned(ev: { kind: TimelineKind; occurred_at: string }, now: Date = new Date()): boolean {
  if (ev.kind !== 'task_due') return false
  const occurredMs = Date.parse(ev.occurred_at)
  if (Number.isNaN(occurredMs)) return false
  return occurredMs > now.getTime()
}

/**
 * eventChipsForDay returns the events to display directly inside a calendar
 * day cell, capped at `max` for readability. The full list is available via
 * the day drawer (DayDrawer); the cell only previews the first few.
 *
 * Pure function: events are filtered to the given local date by string
 * comparison on `dateKey()` output, then truncated. The "+N more" count
 * is the remainder so callers can render `+{moreCount}` without
 * recomputing.
 *
 * Sort order matches the day drawer: chronological (occurred_at ASC) so
 * earlier-in-day events appear higher.
 */
export function eventChipsForDay(
  events: TimelineEvent[],
  dayKey: string,
  /** Local-timezone "YYYY-MM-DD" → built-in dateKey() helper. */
  toDayKey: (d: Date) => string,
  max = 3,
): { shown: TimelineEvent[]; moreCount: number } {
  const filtered = events
    .filter((ev) => toDayKey(new Date(ev.occurred_at)) === dayKey)
    .sort((a, b) => a.occurred_at.localeCompare(b.occurred_at))
  if (filtered.length <= max) {
    return { shown: filtered, moreCount: 0 }
  }
  return { shown: filtered.slice(0, max), moreCount: filtered.length - max }
}

/**
 * truncateTitle clips a string to `max` chars and appends an ellipsis when
 * truncated. Used by MonthGrid to keep cell text on one line. Pure function
 * for easy testing.
 */
export function truncateTitle(title: string, max = 20): string {
  if (title.length <= max) return title
  return title.slice(0, max - 1).trimEnd() + '…'
}
