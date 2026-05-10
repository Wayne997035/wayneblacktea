import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { ProjectCreateModal } from './ProjectCreateModal'
import { useToastStore } from '../../stores/toastStore'
import type { Goal } from '../../types/api'

const mutateAsync = vi.fn()
const useCreateProjectMock = vi.fn()

vi.mock('../../hooks/useProjects', () => ({
  useCreateProject: () => useCreateProjectMock(),
}))

function makeMutation(impl: typeof mutateAsync, isPending = false) {
  return { mutateAsync: impl, isPending }
}

const sampleGoals: Goal[] = [
  { id: 'g1', title: 'Goal A', status: 'active', created_at: '', updated_at: '' },
]

function renderModal({ goals = sampleGoals, onClose = vi.fn() } = {}) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return {
    ...render(
      <QueryClientProvider client={client}>
        <ProjectCreateModal goals={goals} onClose={onClose} />
      </QueryClientProvider>,
    ),
    onClose,
  }
}

beforeEach(() => {
  mutateAsync.mockReset()
  useCreateProjectMock.mockReturnValue(makeMutation(mutateAsync))
  useToastStore.setState({ toasts: [] })
})

afterEach(() => {
  vi.restoreAllMocks()
})

describe('ProjectCreateModal', () => {
  it('renders create title and focuses title input', () => {
    renderModal()
    expect(screen.getByRole('heading', { level: 2 })).toHaveTextContent('Add Project')
    expect(document.activeElement).toBe(
      screen.getByLabelText(/^Title/i, { selector: 'input' }),
    )
  })

  it('shows required errors for title and slug on empty submit', async () => {
    const user = userEvent.setup()
    renderModal()
    await user.click(screen.getByRole('button', { name: 'Add project' }))
    expect(screen.getByText('Title is required')).toBeInTheDocument()
    expect(screen.getByText('Slug is required')).toBeInTheDocument()
    expect(mutateAsync).not.toHaveBeenCalled()
  })

  it('rejects an invalid slug with a clear error', async () => {
    const user = userEvent.setup()
    renderModal()
    await user.type(screen.getByLabelText(/^Title/i, { selector: 'input' }), 'My project')
    await user.type(screen.getByLabelText(/Repo \/ slug name/i), 'Bad Slug')
    await user.click(screen.getByRole('button', { name: 'Add project' }))
    expect(
      screen.getByText('Use lowercase letters, digits, and single hyphens only'),
    ).toBeInTheDocument()
    expect(mutateAsync).not.toHaveBeenCalled()
  })

  it('shows slug suggestion and applies it on click (Project Create only)', async () => {
    const user = userEvent.setup()
    renderModal()
    await user.type(
      screen.getByLabelText(/^Title/i, { selector: 'input' }),
      'My Cool Project',
    )
    const suggestion = screen.getByRole('button', { name: /Suggested: my-cool-project/i })
    expect(suggestion).toBeInTheDocument()
    await user.click(suggestion)
    expect(
      (screen.getByLabelText(/Repo \/ slug name/i) as HTMLInputElement).value,
    ).toBe('my-cool-project')
    // Suggestion should disappear once user has typed (via the click) into the field.
    expect(
      screen.queryByRole('button', { name: /Suggested: my-cool-project/i }),
    ).toBeNull()
  })

  it('submits with trimmed payload + numeric priority + null goal_id', async () => {
    mutateAsync.mockResolvedValue({})
    const user = userEvent.setup()
    const { onClose } = renderModal()
    await user.type(screen.getByLabelText(/^Title/i, { selector: 'input' }), 'Project Title')
    await user.type(screen.getByLabelText(/Repo \/ slug name/i), 'project-slug')
    await user.click(screen.getByRole('button', { name: 'Add project' }))
    await waitFor(() => {
      expect(mutateAsync).toHaveBeenCalledTimes(1)
    })
    expect(mutateAsync).toHaveBeenCalledWith({
      name: 'project-slug',
      title: 'Project Title',
      area: undefined,
      description: undefined,
      goal_id: null,
      priority: 3,
    })
    expect(useToastStore.getState().toasts[0]?.message).toBe('Project created')
    await waitFor(() => expect(onClose).toHaveBeenCalled())
  })

  it('maps 409 conflict to nameExists error', async () => {
    mutateAsync.mockRejectedValue(new Error('409: conflict'))
    const user = userEvent.setup()
    renderModal()
    await user.type(screen.getByLabelText(/^Title/i, { selector: 'input' }), 'X')
    await user.type(screen.getByLabelText(/Repo \/ slug name/i), 'taken-slug')
    await user.click(screen.getByRole('button', { name: 'Add project' }))
    await waitFor(() => {
      expect(
        screen.getByText('A project with this slug already exists'),
      ).toBeInTheDocument()
    })
  })

  it('renders a priority radio group with 5 options', () => {
    renderModal()
    const group = screen.getByRole('radiogroup', { name: 'Priority' })
    expect(group).toBeInTheDocument()
    const radios = screen.getAllByRole('radio')
    expect(radios).toHaveLength(5)
    expect(radios[2]).toHaveAttribute('aria-checked', 'true')
  })

  it('updates selected priority when clicking another level', async () => {
    const user = userEvent.setup()
    renderModal()
    const radios = screen.getAllByRole('radio')
    await user.click(radios[4])
    expect(radios[4]).toHaveAttribute('aria-checked', 'true')
    expect(radios[2]).toHaveAttribute('aria-checked', 'false')
  })

  it('renders linked-goal select only when goals exist', () => {
    renderModal({ goals: [] })
    expect(screen.queryByLabelText(/Linked goal/i)).toBeNull()
  })

  it('confirms before closing dirty form', async () => {
    const confirmSpy = vi.spyOn(window, 'confirm').mockReturnValue(false)
    const user = userEvent.setup()
    const { onClose } = renderModal()
    await user.type(screen.getByLabelText(/^Title/i, { selector: 'input' }), 'X')
    await user.click(screen.getByRole('button', { name: 'Cancel' }))
    expect(confirmSpy).toHaveBeenCalled()
    expect(onClose).not.toHaveBeenCalled()
  })
})
