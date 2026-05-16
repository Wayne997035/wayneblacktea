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
})
