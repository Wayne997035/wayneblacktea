import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { RepoCard } from './RepoCard'
import type { Repo } from '../../types/api'

const baseRepo: Repo = {
  id: 'r1',
  name: 'wayneblacktea',
  status: 'active',
  known_issues: [],
  created_at: '2026-04-01T00:00:00Z',
  updated_at: '2026-04-01T00:00:00Z',
}

function renderCard(repo: Repo) {
  return render(
    <MemoryRouter>
      <RepoCard repo={repo} />
    </MemoryRouter>,
  )
}

describe('RepoCard', () => {
  it('renders the repo name', () => {
    renderCard(baseRepo)
    expect(screen.getByText('wayneblacktea')).toBeInTheDocument()
  })

  // [F0925-25] Color-token reconciliation step 1 (see StaleBadge.test.tsx).
  // Step 2 (index.css: --color-lang-go = #00ADD8, --color-white = #ffffff,
  // the original literal values) is verified via
  // `grep -n '\-\-color-lang-\|--color-white:' src/index.css` — see 收工單.
  it('Go language badge references --color-lang-go / --color-white (was hardcoded #00ADD8/#fff)', () => {
    renderCard({ ...baseRepo, language: 'Go' })
    const badge = screen.getByText('Go')
    expect(badge.style.background).toBe('var(--color-lang-go)')
    expect(badge.style.color).toBe('var(--color-white)')
  })

  it('Java language badge references --color-lang-java (was hardcoded #B07219)', () => {
    renderCard({ ...baseRepo, language: 'Java' })
    const badge = screen.getByText('Java')
    expect(badge.style.background).toBe('var(--color-lang-java)')
  })
})
