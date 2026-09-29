// [F0925-22] KnowledgeCard's URL link must be guarded through safeHref —
// a non-allowlisted scheme must not become a clickable link.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
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

// [F0929-51] Rating and add-to-learning must surface visible, retryable
// errors instead of silently swallowing a failed mutation — same contract
// as ReviewCard.tsx (F0925-20).
describe('KnowledgeCard rating error handling', () => {
  it('shows an alert when rating fails', async () => {
    useUpdateKnowledgeMock.mockReturnValue({
      mutate: vi.fn((_vars, { onError }: { onError: () => void }) => onError()),
      isPending: false,
    })
    const user = userEvent.setup()
    render(<KnowledgeCard item={{ ...baseItem, learning_value: 3 }} />)

    await user.click(screen.getByRole('button', { name: 'Rate 4 out of 5' }))

    expect(screen.getByRole('alert')).toHaveTextContent('Failed to save your rating. Try again.')
    // Retryable: the star buttons stay enabled after the error.
    expect(screen.getByRole('button', { name: 'Rate 4 out of 5' })).not.toBeDisabled()
  })

  it('shows no alert when rating succeeds', async () => {
    useUpdateKnowledgeMock.mockReturnValue({
      mutate: vi.fn(),
      isPending: false,
    })
    const user = userEvent.setup()
    render(<KnowledgeCard item={{ ...baseItem, learning_value: 3 }} />)

    await user.click(screen.getByRole('button', { name: 'Rate 4 out of 5' }))

    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  it('shows the rating error even when the item was never rated and the pointer has left', async () => {
    useUpdateKnowledgeMock.mockReturnValue({
      mutate: vi.fn((_vars, { onError }: { onError: () => void }) => onError()),
      isPending: false,
    })
    const user = userEvent.setup()
    // Never rated: learning_value is null, so InteractiveStarRating starts
    // in its early-return branch (just the "Rate this item?" placeholder).
    render(<KnowledgeCard item={baseItem} />)

    // Hover reveals the star buttons. Fired via fireEvent (not
    // userEvent.hover) so the branch swap under the cursor doesn't confuse
    // userEvent's own pointer-position tracking for the click that follows.
    fireEvent.mouseEnter(screen.getByLabelText('Rate this item'))
    await user.click(screen.getByRole('button', { name: 'Rate 3 out of 5' }))
    // Mutation failed, so the rollback leaves learning_value at null. Once
    // the pointer leaves the star row, hovered also goes back to null, so
    // the component falls back to the early-return branch.
    fireEvent.mouseLeave(screen.getByLabelText(/^Learning value:/))

    expect(screen.getByLabelText('Rate this item')).toBeInTheDocument()
    expect(screen.getByRole('alert')).toHaveTextContent('Failed to save your rating. Try again.')
  })
})

describe('KnowledgeCard add-to-learning error handling', () => {
  it('shows an alert and keeps add-to-learning retryable when it fails', async () => {
    useCreateConceptFromKnowledgeMock.mockReturnValue({
      mutate: vi.fn((_vars, { onError }: { onError: () => void }) => onError()),
      isPending: false,
    })
    const user = userEvent.setup()
    render(<KnowledgeCard item={baseItem} />)

    const addButton = screen.getByRole('button', { name: `Add to learning: ${baseItem.title}` })
    await user.click(addButton)

    expect(screen.getByRole('alert')).toHaveTextContent('Failed to add. Try again.')
    // Retryable: button returns to enabled default state, not stuck pending.
    expect(addButton).not.toBeDisabled()
    // `added` never flips true on the error path.
    expect(addButton).toHaveTextContent('Add to learning')
  })

  it('shows added state and no alert when add-to-learning succeeds', async () => {
    useCreateConceptFromKnowledgeMock.mockReturnValue({
      mutate: vi.fn((_vars, { onSuccess }: { onSuccess: () => void }) => onSuccess()),
      isPending: false,
    })
    const user = userEvent.setup()
    render(<KnowledgeCard item={baseItem} />)

    const addButton = screen.getByRole('button', { name: `Add to learning: ${baseItem.title}` })
    await user.click(addButton)

    expect(await screen.findByText('Added')).toBeInTheDocument()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })
})
