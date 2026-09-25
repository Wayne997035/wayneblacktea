import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { StaleBadge } from './StaleBadge'

describe('StaleBadge', () => {
  it('renders the pill when stale is true', () => {
    render(<StaleBadge stale={true} />)
    expect(screen.getByText('Stale — reconcile needed')).toBeInTheDocument()
  })

  it('renders nothing when stale is false', () => {
    const { container } = render(<StaleBadge stale={false} />)
    expect(container.firstChild).toBeNull()
  })

  it('has accessible aria-label', () => {
    render(<StaleBadge stale={true} />)
    const badge = screen.getByLabelText(/reconcile needed/i)
    expect(badge).toBeInTheDocument()
  })

  // [F0925-25] Color-token reconciliation step 1: jsdom cannot resolve CSS
  // custom properties, so we assert the inline style references the
  // expected var(--x) name. Step 2 (index.css: --color-warning-bg = the
  // original literal #3d1f00) is verified via
  // `grep -n '\-\-color-warning-bg:' src/index.css` — see 收工單.
  it('background references --color-warning-bg (was hardcoded #3d1f00)', () => {
    render(<StaleBadge stale={true} />)
    const badge = screen.getByLabelText(/reconcile needed/i)
    expect(badge.style.background).toBe('var(--color-warning-bg)')
  })
})
