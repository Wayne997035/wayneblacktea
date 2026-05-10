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

// TODO: i18n — currently hard-coded English labels until i18n entries land.
const KIND_LABELS: Record<TimelineKind, string> = {
  task_created:     'Task created',
  task_completed:   'Task completed',
  decision:         'Decision',
  activity:         'Activity',
  knowledge:        'Knowledge',
  concept:          'Concept',
  review_submitted: 'Review',
  handoff_created:  'Handoff opened',
  handoff_resolved: 'Handoff resolved',
}

export function kindLabel(kind: TimelineKind | string): string {
  return KIND_LABELS[kind as TimelineKind] ?? String(kind)
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
