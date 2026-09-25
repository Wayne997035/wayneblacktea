// [F0925-22] KnowledgeCard's URL link must be guarded through safeHref —
// a non-allowlisted scheme must not become a clickable link.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import { KnowledgeCard } from './KnowledgeCard'
import type { KnowledgeItem } from '../../types/api'

const useCreateConceptFromKnowledgeMock = vi.fn()
const useUpdateKnowledgeMock = vi.fn()

vi.mock('../../hooks/useReviews', () => ({
  useCreateConceptFromKnowledge: () => useCreateConceptFromKnowledgeMock(),
}))
vi.mock('../../hooks/useKnowledge', () => ({
  useUpdateKnowledge: () => useUpdateKnowledgeMock(),
}))

const baseItem: KnowledgeItem = {
  id: 'k1',
  type: 'article',
  title: 'Some article',
  content: 'body',
  url: null,
  tags: [],
  created_at: '2026-05-20T00:00:00Z',
  updated_at: '2026-05-20T00:00:00Z',
  source: 'manual',
  learning_value: null,
}

beforeEach(() => {
  useCreateConceptFromKnowledgeMock.mockReturnValue({ mutate: vi.fn(), isPending: false })
  useUpdateKnowledgeMock.mockReturnValue({ mutate: vi.fn(), isPending: false })
})

afterEach(() => {
  vi.restoreAllMocks()
})

describe('KnowledgeCard link safety', () => {
  it('renders no clickable link for a javascript: scheme URL', () => {
    render(<KnowledgeCard item={{ ...baseItem, url: 'javascript:alert(1)' }} />)
    expect(screen.queryByRole('link')).not.toBeInTheDocument()
    // The icon affordance is still present, just inert.
    expect(screen.getByLabelText(`Open link for ${baseItem.title}`)).toBeInTheDocument()
  })

  it('renders a working link with the correct href for an https URL', () => {
    render(<KnowledgeCard item={{ ...baseItem, url: 'https://example.com/a' }} />)
    const link = screen.getByRole('link')
    expect(link).toHaveAttribute('href', 'https://example.com/a')
  })

  it('renders no link when url is null', () => {
    render(<KnowledgeCard item={baseItem} />)
    expect(screen.queryByRole('link')).not.toBeInTheDocument()
  })

  // [F0925-22] Positive control: temporarily reverting KnowledgeCard's href
  // back to the raw `item.url` (bypassing safeHref) must make this test
  // fail, proving it actually exercises the guard. Run manually:
  //   1. In KnowledgeCard.tsx, change `href={href}` back to `href={item.url}`
  //      and drop the `href === '#'` branch.
  //   2. `npx vitest run src/components/knowledge/KnowledgeCard.test.tsx`
  //      → the javascript: test above goes red (finds a role=link).
  //   3. Revert — green again.
  it('positive control: dangerous scheme never gets role=link', () => {
    render(<KnowledgeCard item={{ ...baseItem, url: 'data:text/html,<script>alert(1)</script>' }} />)
    expect(screen.queryByRole('link')).toBeNull()
  })
})
