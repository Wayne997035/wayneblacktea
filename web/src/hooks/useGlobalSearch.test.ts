import { describe, it, expect } from 'vitest'
import { isSafeRelativeUrl } from './useGlobalSearch'

describe('isSafeRelativeUrl', () => {
  it.each([
    ['/', true],
    ['/knowledge', true],
    ['/decisions/abc', true],
    ['/gtd?id=1', true],
  ])('accepts in-app path %s', (url, want) => {
    expect(isSafeRelativeUrl(url)).toBe(want)
  })

  it.each([
    // Open-redirect bypass attempts.
    ['//evil.com', false],
    ['//evil.com/phishing', false],
    ['/\\evil.com', false],
    ['/\\\\evil.com', false],
    // Absolute URLs.
    ['https://evil.com', false],
    ['http://evil.com', false],
    // JS / data scheme.
    ['javascript:alert(1)', false],
    ['data:text/html,<h1>x</h1>', false],
    // Empty / whitespace.
    ['', false],
    [' /knowledge', false],
  ])('rejects %s', (url, want) => {
    expect(isSafeRelativeUrl(url)).toBe(want)
  })
})
