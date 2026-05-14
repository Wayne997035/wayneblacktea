import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { GoalModal } from './GoalModal'
import { useToastStore } from '../../stores/toastStore'
import type { Goal } from '../../types/api'

const createMutateAsync = vi.fn()
const updateMutateAsync = vi.fn()
const useCreateGoalMock = vi.fn()
const useUpdateGoalMock = vi.fn()

vi.mock('../../hooks/useGoals', () => ({
  useCreateGoal: () => useCreateGoalMock(),
  useUpdateGoal: () => useUpdateGoalMock(),
}))

function makeMutation(impl: typeof createMutateAsync, isPending = false) {
  return { mutateAsync: impl, isPending }
}

const sampleGoal: Goal = {
  id: 'goal-1',
  title: 'Run a marathon',
  area: 'Health',
  description: 'Complete a full marathon',
  status: 'active',
  due_date: '2026-12-31T00:00:00.000Z',
  created_at: '2026-01-01T00:00:00.000Z',
  updated_at: '2026-01-01T00:00:00.000Z',
}

function renderCreateModal(onClose = vi.fn()) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return {
    ...render(
      <QueryClientProvider client={client}>
        <GoalModal onClose={onClose} />
      </QueryClientProvider>,
    ),
    onClose,
  }
}

function renderEditModal(entity = sampleGoal, onClose = vi.fn()) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return {
    ...render(
      <QueryClientProvider client={client}>
        <GoalModal entity={entity} onClose={onClose} />
      </QueryClientProvider>,
    ),
    onClose,
  }
}

beforeEach(() => {
  createMutateAsync.mockReset()
  updateMutateAsync.mockReset()
  useCreateGoalMock.mockReturnValue(makeMutation(createMutateAsync))
  useUpdateGoalMock.mockReturnValue(makeMutation(updateMutateAsync))
  useToastStore.setState({ toasts: [] })
})

afterEach(() => {
  vi.restoreAllMocks()
})

describe('GoalModal — Create mode', () => {
  it('renders create title and focuses title input', () => {
    renderCreateModal()
    expect(screen.getByRole('heading', { level: 2 })).toHaveTextContent('Add Goal')
    const titleInput = screen.getByLabelText(/Title/i, { selector: 'input' })
    expect(document.activeElement).toBe(titleInput)
  })

  it('shows required-field error when submitting an empty form', async () => {
    const user = userEvent.setup()
    renderCreateModal()
    await user.click(screen.getByRole('button', { name: 'Add goal' }))
    expect(screen.getByText('Title is required')).toBeInTheDocument()
    expect(createMutateAsync).not.toHaveBeenCalled()
  })

  it('clears field error once the user fixes the value', async () => {
    const user = userEvent.setup()
    renderCreateModal()
    await user.click(screen.getByRole('button', { name: 'Add goal' }))
    expect(screen.getByText('Title is required')).toBeInTheDocument()
    await user.type(screen.getByLabelText(/Title/i, { selector: 'input' }), 'New goal')
    expect(screen.queryByText('Title is required')).toBeNull()
  })

  it('shows titleTooLong error for 201-char title', async () => {
    const user = userEvent.setup()
    renderCreateModal()
    const titleInput = screen.getByLabelText(/Title/i, { selector: 'input' })
    // maxLength=200 prevents typing more than 200 chars, so we set value directly
    await user.type(titleInput, 'a'.repeat(200))
    // The input has maxLength=200, so we need to remove maxLength and type 201 chars
    // Instead: directly fire change event
    const nativeInputValueSetter = Object.getOwnPropertyDescriptor(
      window.HTMLInputElement.prototype,
      'value',
    )?.set
    nativeInputValueSetter?.call(titleInput, 'a'.repeat(201))
    titleInput.dispatchEvent(new Event('input', { bubbles: true }))
    await user.click(screen.getByRole('button', { name: 'Add goal' }))
    expect(screen.getByText('Title must be 200 characters or fewer')).toBeInTheDocument()
    expect(createMutateAsync).not.toHaveBeenCalled()
  })

  it('submits with trimmed payload, fires toast, closes dialog on success', async () => {
    createMutateAsync.mockResolvedValue({})
    const user = userEvent.setup()
    const { onClose } = renderCreateModal()
    await user.type(screen.getByLabelText(/Title/i, { selector: 'input' }), '  Run a marathon  ')
    await user.type(screen.getByLabelText(/Area/i, { selector: 'input' }), ' Health ')
    await user.click(screen.getByRole('button', { name: 'Add goal' }))
    await waitFor(() => expect(createMutateAsync).toHaveBeenCalledTimes(1))
    expect(createMutateAsync).toHaveBeenCalledWith(
      expect.objectContaining({ title: 'Run a marathon', area: 'Health' }),
    )
    expect(useToastStore.getState().toasts[0]?.message).toBe('Goal created')
    await waitFor(() => expect(onClose).toHaveBeenCalled())
  })

  it('shows save-failed error on 5xx and keeps dialog open', async () => {
    createMutateAsync.mockRejectedValue(new Error('500: server error'))
    const user = userEvent.setup()
    const { onClose } = renderCreateModal()
    await user.type(screen.getByLabelText(/Title/i, { selector: 'input' }), 'My goal')
    await user.click(screen.getByRole('button', { name: 'Add goal' }))
    await waitFor(() => {
      expect(screen.getByText('Could not save. Try again.')).toBeInTheDocument()
    })
    expect(useToastStore.getState().toasts).toHaveLength(0)
    expect(onClose).not.toHaveBeenCalled()
  })

  it('closes immediately on Cancel when form is pristine', async () => {
    const user = userEvent.setup()
    const { onClose } = renderCreateModal()
    await user.click(screen.getByRole('button', { name: 'Cancel' }))
    await waitFor(() => expect(onClose).toHaveBeenCalled())
  })

  it('prompts window.confirm before close when form is dirty', async () => {
    const confirmSpy = vi.spyOn(window, 'confirm').mockReturnValue(false)
    const user = userEvent.setup()
    const { onClose } = renderCreateModal()
    await user.type(screen.getByLabelText(/Title/i, { selector: 'input' }), 'Dirty')
    await user.click(screen.getByRole('button', { name: 'Cancel' }))
    expect(confirmSpy).toHaveBeenCalledWith('Discard your changes?')
    expect(onClose).not.toHaveBeenCalled()

    confirmSpy.mockReturnValue(true)
    await user.click(screen.getByRole('button', { name: 'Cancel' }))
    await waitFor(() => expect(onClose).toHaveBeenCalled())
  })

  it('renders char counter on description textarea', async () => {
    const user = userEvent.setup()
    renderCreateModal()
    const desc = screen.getByLabelText(/Description/i)
    await user.type(desc, 'hello')
    expect(screen.getByText('5 / 2000')).toBeInTheDocument()
  })

  it('returns focus to the triggering element after close (WCAG 2.4.3)', async () => {
    const trigger = document.createElement('button')
    trigger.textContent = 'Trigger'
    document.body.appendChild(trigger)
    trigger.focus()
    expect(document.activeElement).toBe(trigger)

    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(
      <QueryClientProvider client={client}>
        <GoalModal onClose={() => {}} />
      </QueryClientProvider>,
    )
    const titleInput = screen.getByLabelText(/Title/i, { selector: 'input' })
    expect(document.activeElement).toBe(titleInput)

    const user = userEvent.setup()
    await user.click(screen.getByRole('button', { name: 'Cancel' }))
    expect(document.activeElement).toBe(trigger)
    document.body.removeChild(trigger)
  })
})

describe('GoalModal — Edit mode', () => {
  it('renders edit title and pre-fills fields from entity', () => {
    renderEditModal()
    expect(screen.getByRole('heading', { level: 2 })).toHaveTextContent('Edit Goal')
    expect((screen.getByLabelText(/Title/i, { selector: 'input' }) as HTMLInputElement).value).toBe('Run a marathon')
    expect((screen.getByLabelText(/Area/i, { selector: 'input' }) as HTMLInputElement).value).toBe('Health')
    const statusSelect = screen.getByLabelText(/Status/i, { selector: 'select' }) as HTMLSelectElement
    expect(statusSelect.value).toBe('active')
  })

  it('submit button is disabled when form is pristine in Edit mode', () => {
    renderEditModal()
    const submitBtn = screen.getByRole('button', { name: 'Save changes' })
    expect(submitBtn).toBeDisabled()
  })

  it('submit button becomes enabled after changing a field', async () => {
    const user = userEvent.setup()
    renderEditModal()
    const titleInput = screen.getByLabelText(/Title/i, { selector: 'input' })
    await user.clear(titleInput)
    await user.type(titleInput, 'Updated title')
    const submitBtn = screen.getByRole('button', { name: 'Save changes' })
    expect(submitBtn).not.toBeDisabled()
  })

  it('calls useUpdateGoal on submit with entity id and updated payload', async () => {
    updateMutateAsync.mockResolvedValue({})
    const user = userEvent.setup()
    const { onClose } = renderEditModal()
    const titleInput = screen.getByLabelText(/Title/i, { selector: 'input' })
    await user.clear(titleInput)
    await user.type(titleInput, 'Updated marathon')
    await user.click(screen.getByRole('button', { name: 'Save changes' }))
    await waitFor(() => expect(updateMutateAsync).toHaveBeenCalledTimes(1))
    expect(updateMutateAsync).toHaveBeenCalledWith(
      expect.objectContaining({ id: 'goal-1', title: 'Updated marathon' }),
    )
    expect(useToastStore.getState().toasts[0]?.message).toBe('Goal updated')
    await waitFor(() => expect(onClose).toHaveBeenCalled())
  })

  it('does not call createGoal in Edit mode', async () => {
    updateMutateAsync.mockResolvedValue({})
    const user = userEvent.setup()
    renderEditModal()
    const titleInput = screen.getByLabelText(/Title/i, { selector: 'input' })
    await user.clear(titleInput)
    await user.type(titleInput, 'Changed')
    await user.click(screen.getByRole('button', { name: 'Save changes' }))
    await waitFor(() => expect(updateMutateAsync).toHaveBeenCalled())
    expect(createMutateAsync).not.toHaveBeenCalled()
  })

  it('shows save-failed error on update 5xx', async () => {
    updateMutateAsync.mockRejectedValue(new Error('500: error'))
    const user = userEvent.setup()
    const { onClose } = renderEditModal()
    const titleInput = screen.getByLabelText(/Title/i, { selector: 'input' })
    await user.clear(titleInput)
    await user.type(titleInput, 'Changed')
    await user.click(screen.getByRole('button', { name: 'Save changes' }))
    await waitFor(() => {
      expect(screen.getByText('Could not save. Try again.')).toBeInTheDocument()
    })
    expect(onClose).not.toHaveBeenCalled()
  })

  it('shows status select with all GoalStatus options', () => {
    renderEditModal()
    const select = screen.getByLabelText(/Status/i, { selector: 'select' }) as HTMLSelectElement
    const options = Array.from(select.options).map((o) => o.value)
    expect(options).toEqual(['active', 'completed', 'archived'])
  })
})
