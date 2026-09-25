import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { SourceBadge } from './SourceBadge'

describe('SourceBadge', () => {
  it('renders the type label', () => {
    render(<SourceBadge type="article" />)
    expect(screen.getByText('Article')).toBeInTheDocument()
  })

  // [F0925-25] Color-token reconciliation step 1 (see StaleBadge.test.tsx).
  // Step 2 (index.css values for the 5 source-type variants = the original
  // literal rgba/hex values) is verified via
  // `grep -n '\-\-color-source-' src/index.css` — see 收工單.
  it('article variant references --color-source-article-* (was hardcoded rgba/hex)', () => {
    render(<SourceBadge type="article" />)
    const button = screen.getByText('Article').closest('button')!
    expect(button.style.background).toBe('var(--color-source-article-bg)')
    expect(button.style.color).toBe('var(--color-accent-blue)')
    expect(button.style.border).toContain('var(--color-source-article-border)')
  })

  it('agent-proposed variant references --color-source-agent-* (was hardcoded rgba/hex)', () => {
    render(<SourceBadge type="agent-proposed" />)
    const button = screen.getByText('Agent').closest('button')!
    expect(button.style.background).toBe('var(--color-source-agent-bg)')
    expect(button.style.color).toBe('var(--color-source-agent-text)')
  })
})
