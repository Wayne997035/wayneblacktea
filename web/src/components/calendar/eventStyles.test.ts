import { describe, it, expect } from 'vitest'
import { kindColor, kindLabel, ALL_KINDS } from './eventStyles'
import type { TimelineKind } from '../../types/api'

describe('kindColor', () => {
  it.each<TimelineKind>([
    'task_created',
    'task_completed',
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

describe('kindLabel', () => {
  it('returns a non-empty label for every known kind', () => {
    for (const k of ALL_KINDS) {
      expect(kindLabel(k).length).toBeGreaterThan(0)
    }
  })

  it('returns the raw kind string for unknown kinds', () => {
    expect(kindLabel('mystery' as unknown as TimelineKind)).toBe('mystery')
  })
})

describe('ALL_KINDS', () => {
  it('lists all 9 kinds', () => {
    expect(ALL_KINDS).toHaveLength(9)
  })
})
