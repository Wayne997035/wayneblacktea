// [F0925-22] Shared href-scheme allowlist guard. See file header of
// safeHref.ts for the hardening rules being asserted here.
import { describe, it, expect } from 'vitest'
import { safeHref } from './safeHref'

describe('safeHref', () => {
  it.each([
    ['javascript:alert(1)'],
    ['JaVaScRiPt:x'],
    ['data:text/html,x'],
    ['\thttps://a'],
    ['https://a\nevil'],
  ])('neutralises %s to "#"', (value) => {
    expect(safeHref(value)).toBe('#')
  })

  it.each([
    ['https://a'],
    ['HTTPS://a'],
    ['wbt://tasks/x'],
  ])('preserves the allowlisted value %s unchanged', (value) => {
    expect(safeHref(value)).toBe(value)
  })

  it('returns "#" for undefined', () => {
    expect(safeHref(undefined)).toBe('#')
  })

  it('returns "#" for an empty string', () => {
    expect(safeHref('')).toBe('#')
  })
})
