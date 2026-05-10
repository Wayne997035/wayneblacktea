import { describe, it, expect } from 'vitest'
import {
  kindColor,
  kindLabelKey,
  ALL_KINDS,
  isPlanned,
  eventChipsForDay,
  truncateTitle,
} from './eventStyles'
import { dateKey } from './dateUtils'
import type { TimelineEvent, TimelineKind } from '../../types/api'

describe('kindColor', () => {
  it.each<TimelineKind>([
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
  ])('returns dot/text/ring class strings for %s', (kind) => {
    const c = kindColor(kind)
    expect(c.dot).toMatch(/^bg-/)
    expect(c.text).toMatch(/^text-/)
    expect(c.ring).toMatch(/^ring-/)
  })

  it('falls back to gray for unknown kinds', () => {
    const c = kindColor('something_new' as unknown as TimelineKind)
    expect(c.dot).toBe('bg-gray-400')
    expect(c.text).toBe('text-gray-600')
  })

  it('maps task_created and task_completed to green family', () => {
    expect(kindColor('task_created').dot).toBe('bg-green-500')
    expect(kindColor('task_completed').dot).toBe('bg-green-500')
  })

  it('maps task_due to orange family (distinct from green completed)', () => {
    expect(kindColor('task_due').dot).toBe('bg-orange-500')
    expect(kindColor('task_due').text).toBe('text-orange-700')
  })

  it('maps decision to yellow family', () => {
    expect(kindColor('decision').dot).toBe('bg-yellow-500')
  })

  it('maps knowledge and concept to blue family', () => {
    expect(kindColor('knowledge').dot).toBe('bg-blue-500')
    expect(kindColor('concept').dot).toBe('bg-blue-500')
  })

  it('maps review_submitted to purple family', () => {
    expect(kindColor('review_submitted').dot).toBe('bg-purple-500')
  })

  it('maps activity / handoff_* to gray family', () => {
    expect(kindColor('activity').dot).toBe('bg-gray-500')
    expect(kindColor('handoff_created').dot).toBe('bg-gray-500')
    expect(kindColor('handoff_resolved').dot).toBe('bg-gray-500')
  })
})

describe('kindLabelKey', () => {
  it('returns the calendar.kinds.* i18n key for every known kind', () => {
    for (const k of ALL_KINDS) {
      expect(kindLabelKey(k)).toBe(`calendar.kinds.${k}`)
    }
  })

  it('returns calendar.kinds.task_due for the new planning kind', () => {
    expect(kindLabelKey('task_due')).toBe('calendar.kinds.task_due')
  })

  it('returns the raw kind string for unknown kinds (so t() leaves it untranslated)', () => {
    expect(kindLabelKey('mystery' as unknown as TimelineKind)).toBe('mystery')
  })
})

describe('ALL_KINDS', () => {
  it('lists all 10 kinds (task_due added)', () => {
    expect(ALL_KINDS).toHaveLength(10)
    expect(ALL_KINDS).toContain('task_due')
  })
})

describe('isPlanned', () => {
  const now = new Date('2026-05-10T12:00:00Z')

  it('returns true for task_due with occurred_at strictly in the future', () => {
    expect(
      isPlanned({ kind: 'task_due', occurred_at: '2026-05-12T00:00:00Z' }, now),
    ).toBe(true)
  })

  it('returns false for task_due with occurred_at in the past (overdue)', () => {
    expect(
      isPlanned({ kind: 'task_due', occurred_at: '2026-05-08T00:00:00Z' }, now),
    ).toBe(false)
  })

  it('returns false for task_due with occurred_at exactly equal to now (boundary)', () => {
    expect(
      isPlanned({ kind: 'task_due', occurred_at: now.toISOString() }, now),
    ).toBe(false)
  })

  it('returns false for non-task_due kinds even if occurred_at is in the future', () => {
    expect(
      isPlanned({ kind: 'task_completed', occurred_at: '2027-01-01T00:00:00Z' }, now),
    ).toBe(false)
    expect(
      isPlanned({ kind: 'decision', occurred_at: '2027-01-01T00:00:00Z' }, now),
    ).toBe(false)
  })

  it('returns false for invalid date strings', () => {
    expect(
      isPlanned({ kind: 'task_due', occurred_at: 'not-a-date' }, now),
    ).toBe(false)
  })
})

function evt(kind: TimelineKind, occurredAt: string, title = `${kind} title`): TimelineEvent {
  return { kind, occurred_at: occurredAt, ref_id: `${kind}-${occurredAt}`, title }
}

describe('eventChipsForDay', () => {
  it('returns events whose dateKey matches, sorted ASC by occurred_at', () => {
    const events: TimelineEvent[] = [
      evt('decision',     '2026-05-10T15:00:00Z', 'late'),
      evt('task_created', '2026-05-10T09:00:00Z', 'early'),
      evt('decision',     '2026-05-11T09:00:00Z', 'next-day'),
    ]
    const target = dateKey(new Date('2026-05-10T12:00:00Z'))
    const { shown, moreCount } = eventChipsForDay(events, target, dateKey, 5)
    expect(shown).toHaveLength(2)
    expect(shown[0].title).toBe('early')
    expect(shown[1].title).toBe('late')
    expect(moreCount).toBe(0)
  })

  it('caps shown at max and reports overflow as moreCount', () => {
    const events: TimelineEvent[] = [
      evt('decision', '2026-05-10T01:00:00Z', 'one'),
      evt('decision', '2026-05-10T02:00:00Z', 'two'),
      evt('decision', '2026-05-10T03:00:00Z', 'three'),
      evt('decision', '2026-05-10T04:00:00Z', 'four'),
      evt('decision', '2026-05-10T05:00:00Z', 'five'),
    ]
    const target = dateKey(new Date('2026-05-10T12:00:00Z'))
    const { shown, moreCount } = eventChipsForDay(events, target, dateKey, 3)
    expect(shown).toHaveLength(3)
    expect(moreCount).toBe(2)
  })

  it('returns empty shown and zero overflow when no events match', () => {
    const target = dateKey(new Date('2026-05-10T12:00:00Z'))
    const { shown, moreCount } = eventChipsForDay([], target, dateKey, 3)
    expect(shown).toHaveLength(0)
    expect(moreCount).toBe(0)
  })

  it('uses default max=3 when caller omits the argument', () => {
    const events: TimelineEvent[] = Array.from({ length: 5 }, (_, i) =>
      evt('decision', `2026-05-10T0${i + 1}:00:00Z`, `e${i}`),
    )
    const target = dateKey(new Date('2026-05-10T12:00:00Z'))
    const { shown, moreCount } = eventChipsForDay(events, target, dateKey)
    expect(shown).toHaveLength(3)
    expect(moreCount).toBe(2)
  })
})

describe('truncateTitle', () => {
  it('returns the original string when shorter than max', () => {
    expect(truncateTitle('short', 20)).toBe('short')
  })

  it('truncates with ellipsis when longer than max', () => {
    const out = truncateTitle('this is a really really long title', 20)
    expect(out).toHaveLength(20)
    expect(out.endsWith('…')).toBe(true)
  })

  it('returns the original string when exactly equal to max', () => {
    expect(truncateTitle('twenty-char title!!!', 20)).toBe('twenty-char title!!!')
  })

  it('uses default max=20 when caller omits the argument', () => {
    const out = truncateTitle('a'.repeat(30))
    expect(out).toHaveLength(20)
    expect(out.endsWith('…')).toBe(true)
  })
})
