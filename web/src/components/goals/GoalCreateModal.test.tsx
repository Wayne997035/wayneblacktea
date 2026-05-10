import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { GoalCreateModal } from './GoalCreateModal'
import { useToastStore } from '../../stores/toastStore'

const mutateAsync = vi.fn()
const useCreateGoalMock = vi.fn()

vi.mock('../../hooks/useGoals', () => ({
  useCreateGoal: () => useCreateGoalMock(),
}))

function makeMutation(impl: typeof mutateAsync, isPending = false) {
  return { mutateAsync: impl, isPending }
}

function renderModal(onClose = vi.fn()) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return {
    ...render(
      <QueryClientProvider client={client}>
        <GoalCreateModal onClose={onClose} />
      </QueryClientProvider>,
    ),
    onClose,
  }
}

beforeEach(() => {
  mutateAsync.mockReset()
  useCreateGoalMock.mockReturnValue(makeMutation(mutateAsync))
  useToastStore.setState({ toasts: [] })
})

afterEach(() => {
  vi.restoreAllMocks()
})

describe('GoalCreateModal', () => {
  it('renders the create title and focuses the title input on open', () => {
    renderModal()
    expect(screen.getByRole('heading', { level: 2 })).toHaveTextContent('Add Goal')
    const titleInput = screen.getByLabelText(/Title/i, { selector: 'input' })
    expect(document.activeElement).toBe(titleInput)
  })

  it('shows required-field error when submitting an empty form', async () => {
    const user = userEvent.setup()
    renderModal()
    await user.click(screen.getByRole('button', { name: 'Add goal' }))
    expect(screen.getByText('Title is required')).toBeInTheDocument()
    expect(mutateAsync).not.toHaveBeenCalled()
  })

  it('clears field error once the user fixes the value', async () => {
    const user = userEvent.setup()
    renderModal()
    await user.click(screen.getByRole('button', { name: 'Add goal' }))
    expect(screen.getByText('Title is required')).toBeInTheDocument()
    await user.type(
      screen.getByLabelText(/Title/i, { selector: 'input' }),
      'New goal',
    )
    expect(screen.queryByText('Title is required')).toBeNull()
  })

  it('submits with trimmed payload, fires toast, closes dialog', async () => {
    mutateAsync.mockResolvedValue({})
    const user = userEvent.setup()
    const { onClose } = renderModal()
    await user.type(
      screen.getByLabelText(/Title/i, { selector: 'input' }),
      '  Run a marathon  ',
    )
    await user.type(
      screen.getByLabelText(/Area/i, { selector: 'input' }),
      ' Health ',
    )
    await user.click(screen.getByRole('button', { name: 'Add goal' }))
    await waitFor(() => {
      expect(mutateAsync).toHaveBeenCalledTimes(1)
    })
    expect(mutateAsync).toHaveBeenCalledWith({
      title: 'Run a marathon',
      area: 'Health',
      description: undefined,
      due_date: null,
    })
    expect(useToastStore.getState().toasts).toHaveLength(1)
    expect(useToastStore.getState().toasts[0].message).toBe('Goal created')
    await waitFor(() => expect(onClose).toHaveBeenCalled())
  })

  it('shows generic save-failed message on 5xx error and keeps dialog open', async () => {
    mutateAsync.mockRejectedValue(new Error('500: server'))
    const user = userEvent.setup()
    const { onClose } = renderModal()
    await user.type(
      screen.getByLabelText(/Title/i, { selector: 'input' }),
      'My goal',
    )
    await user.click(screen.getByRole('button', { name: 'Add goal' }))
    await waitFor(() => {
      expect(screen.getByText('Could not save. Try again.')).toBeInTheDocument()
    })
    expect(useToastStore.getState().toasts).toHaveLength(0)
    expect(onClose).not.toHaveBeenCalled()
  })

  it('closes immediately on Cancel when form is pristine', async () => {
    const user = userEvent.setup()
    const { onClose } = renderModal()
    await user.click(screen.getByRole('button', { name: 'Cancel' }))
    await waitFor(() => expect(onClose).toHaveBeenCalled())
  })

  it('prompts window.confirm before close when form is dirty', async () => {
    const confirmSpy = vi.spyOn(window, 'confirm').mockReturnValue(false)
    const user = userEvent.setup()
    const { onClose } = renderModal()
    await user.type(
      screen.getByLabelText(/Title/i, { selector: 'input' }),
      'Dirty',
    )
    await user.click(screen.getByRole('button', { name: 'Cancel' }))
    expect(confirmSpy).toHaveBeenCalledWith('Discard your changes?')
    expect(onClose).not.toHaveBeenCalled()

    confirmSpy.mockReturnValue(true)
    await user.click(screen.getByRole('button', { name: 'Cancel' }))
    expect(confirmSpy).toHaveBeenCalledTimes(2)
    await waitFor(() => expect(onClose).toHaveBeenCalled())
  })

  it('returns focus to the triggering element after close (WCAG 2.4.3)', async () => {
    // Add a trigger button to the body and focus it BEFORE rendering the modal,
    // so the dialog polyfill captures it as the original trigger.
    const trigger = document.createElement('button')
    trigger.textContent = 'Trigger'
    document.body.appendChild(trigger)
    trigger.focus()
    expect(document.activeElement).toBe(trigger)

    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(
      <QueryClientProvider client={client}>
        <GoalCreateModal onClose={() => {}} />
      </QueryClientProvider>,
    )
    // After mount, autofocus moves focus to the title input.
    const titleInput = screen.getByLabelText(/Title/i, { selector: 'input' })
    expect(document.activeElement).toBe(titleInput)

    // Close via Cancel — pristine form, no confirm prompt.
    const user = userEvent.setup()
    await user.click(screen.getByRole('button', { name: 'Cancel' }))
    // Focus must return to the trigger that opened the dialog.
    expect(document.activeElement).toBe(trigger)
    document.body.removeChild(trigger)
  })

  it('renders char counter on description', async () => {
    const user = userEvent.setup()
    renderModal()
    const desc = screen.getByLabelText(/Description/i)
    await user.type(desc, 'hello')
    expect(screen.getByText('5 / 2000')).toBeInTheDocument()
  })
})
