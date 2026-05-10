import type { TimelineKind } from '../../types/api'

/**
 * KIND_GROUPS maps each kind to a colour family used both as Tailwind
 * background ("dot" colour) and a darker text variant for badges.
 */
const KIND_STYLES: Record<TimelineKind, { dot: string; text: string; ring: string }> = {
  task_created:    { dot: 'bg-green-500',  text: 'text-green-700',  ring: 'ring-green-500' },
  task_completed:  { dot: 'bg-green-500',  text: 'text-green-700',  ring: 'ring-green-500' },
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
  'decision',
  'activity',
  'knowledge',
  'concept',
  'review_submitted',
  'handoff_created',
  'handoff_resolved',
]
