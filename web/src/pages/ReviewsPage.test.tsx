// [F0925-21] SuggestionItem's "已加入" must only render after onAdd's
// promise resolves; a rejection must surface a visible, retryable error
// and leave the "加入學習" button enabled.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { ReviewsPage } from './ReviewsPage'
import type { LearningSuggestion } from '../types/api'

const addFromKnowledgeMutateAsync = vi.fn()
const createConceptMutateAsync = vi.fn()
const useReviewsMock = vi.fn()
const useCreateConceptMock = vi.fn()
const useLearningSuggestionsMock = vi.fn()
const useCreateConceptFromKnowledgeMock = vi.fn()

vi.mock('../hooks/useReviews', () => ({
  useReviews: () => useReviewsMock(),
  useCreateConcept: () => useCreateConceptMock(),
  useLearningSuggestions: () => useLearningSuggestionsMock(),
  useCreateConceptFromKnowledge: () => useCreateConceptFromKnowledgeMock(),
}))

const suggestion: LearningSuggestion = {
  id: 'k1',
  title: 'Some knowledge item',
  content: 'content body',
  tags: [],
}

beforeEach(() => {
  addFromKnowledgeMutateAsync.mockReset()
  createConceptMutateAsync.mockReset()
  useReviewsMock.mockReturnValue({ data: [], isLoading: false, isError: false })
  useCreateConceptMock.mockReturnValue({
    mutate: vi.fn(),
    mutateAsync: createConceptMutateAsync,
    isPending: false,
    isError: false,
  })
  useLearningSuggestionsMock.mockReturnValue({
    data: { knowledge_items: [suggestion], decisions: [] },
    isLoading: false,
    isError: false,
  })
  useCreateConceptFromKnowledgeMock.mockReturnValue({
    mutateAsync: addFromKnowledgeMutateAsync,
    isPending: false,
  })
})

afterEach(() => {
  vi.restoreAllMocks()
})

describe('ReviewsPage — AI suggestions add flow', () => {
  it('shows "已加入" only after the add mutation resolves', async () => {
    addFromKnowledgeMutateAsync.mockResolvedValue({})
    const user = userEvent.setup()
    render(<ReviewsPage />)

    const button = await screen.findByRole('button', { name: `Add to learning: ${suggestion.title}` })
    await user.click(button)

    // [F0925-24] Chinese literal replaced by i18n; en.json's translated value asserted here.
    await waitFor(() => expect(screen.getByText('Added')).toBeInTheDocument())
  })

  it('does NOT show "已加入" and shows a retryable error when the add mutation rejects', async () => {
    addFromKnowledgeMutateAsync.mockRejectedValue(new Error('500'))
    const user = userEvent.setup()
    render(<ReviewsPage />)

    const button = await screen.findByRole('button', { name: `Add to learning: ${suggestion.title}` })
    await user.click(button)

    await waitFor(() => expect(screen.getByText('Failed to add. Try again.')).toBeInTheDocument())
    // [F0925-24] Chinese literal replaced by i18n; en.json's translated value asserted here.
    expect(screen.queryByText('Added')).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: `Add to learning: ${suggestion.title}` })).not.toBeDisabled()
  })
})
