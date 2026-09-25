// [F0925-20] ReviewCard must only show "已記錄" after the backend confirms
// the write; a failed submit must show a visible, retryable error and keep
// the rating buttons available.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { ReviewCard } from './ReviewCard'
import type { DueReview } from '../../types/api'

const submitReviewMock = vi.fn()
const useSubmitReviewMock = vi.fn()

vi.mock('../../hooks/useReviews', () => ({
  useSubmitReview: () => useSubmitReviewMock(),
}))

function makeMutation(impl: typeof submitReviewMock, overrides: Record<string, unknown> = {}) {
  return { mutate: impl, isPending: false, variables: undefined, ...overrides }
}

const review: DueReview = {
  concept_id: 'c1',
  schedule_id: 's1',
  title: 'Test concept',
  content: 'Some content to review',
  stability: 2,
  difficulty: 3,
  due_date: '2026-09-20T00:00:00Z',
  review_count: 1,
}

beforeEach(() => {
  submitReviewMock.mockReset()
  useSubmitReviewMock.mockReturnValue(makeMutation(submitReviewMock))
})

afterEach(() => {
  vi.restoreAllMocks()
})

describe('ReviewCard', () => {
  it('shows "已記錄" only after the API confirms success', async () => {
    submitReviewMock.mockImplementation((_vars, { onSuccess }: { onSuccess: () => void }) => {
      onSuccess()
    })
    const user = userEvent.setup()
    render(<ReviewCard review={review} />)

    await user.click(screen.getByRole('button', { name: 'Good' }))

    await waitFor(() => expect(screen.getByText(/已記錄/)).toBeInTheDocument())
  })

  it('does NOT show "已記錄" and shows a retryable error when the API fails', async () => {
    submitReviewMock.mockImplementation((_vars, { onError }: { onError: () => void }) => {
      onError()
    })
    const user = userEvent.setup()
    render(<ReviewCard review={review} />)

    await user.click(screen.getByRole('button', { name: 'Good' }))

    await waitFor(() => expect(screen.getByText('Failed to save your rating. Try again.')).toBeInTheDocument())
    expect(screen.queryByText(/已記錄/)).not.toBeInTheDocument()
    // Rating buttons remain so the user can retry.
    expect(screen.getByRole('button', { name: 'Good' })).toBeInTheDocument()
  })

  // [F0925-20] Positive control: temporarily restoring the pre-fix
  // unconditional setRated() call must make this test fail, proving the
  // test actually exercises the guard. Run manually (not part of CI):
  //   1. In ReviewCard.tsx, move `setRated(...)` back out of onSuccess so it
  //      runs unconditionally after submitReview(), like onError test above.
  //   2. `npx vitest run src/components/reviews/ReviewCard.test.tsx` → this
  //      test goes red (finds "已記錄" after a failed submit).
  //   3. Revert — this test goes green again.
  it('positive control: error path stays on the rating buttons (no false success)', async () => {
    submitReviewMock.mockImplementation((_vars, { onError }: { onError: () => void }) => {
      onError()
    })
    const user = userEvent.setup()
    render(<ReviewCard review={review} />)

    await user.click(screen.getByRole('button', { name: 'Easy' }))

    await waitFor(() => expect(screen.getByRole('alert')).toBeInTheDocument())
    expect(screen.queryByRole('status')).not.toBeInTheDocument()
  })
})
