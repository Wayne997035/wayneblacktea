import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { ProjectModal } from './ProjectModal'
import { useToastStore } from '../../stores/toastStore'
import type { Goal, Project } from '../../types/api'

const createMutateAsync = vi.fn()
const updateMutateAsync = vi.fn()
const useCreateProjectMock = vi.fn()
const useUpdateProjectMock = vi.fn()

vi.mock('../../hooks/useProjects', () => ({
  useCreateProject: () => useCreateProjectMock(),
  useUpdateProject: () => useUpdateProjectMock(),
}))

function makeMutation(impl: typeof createMutateAsync, isPending = false) {
  return { mutateAsync: impl, isPending }
}

const sampleGoals: Goal[] = [
  { id: 'g1', title: 'Goal A', status: 'active', created_at: '', updated_at: '' },
]

const sampleProject: Project = {
  id: 'proj-1',
  name: 'my-project',
  title: 'My Project',
  description: 'A cool project',
  area: 'Engineering',
  status: 'active',
  priority: 3,
  goal_id: 'g1',
  created_at: '2026-01-01T00:00:00.000Z',
  updated_at: '2026-01-01T00:00:00.000Z',
}

function renderCreateModal({ goals = sampleGoals, onClose = vi.fn() } = {}) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return {
    ...render(
      <QueryClientProvider client={client}>
        <ProjectModal goals={goals} onClose={onClose} />
      </QueryClientProvider>,
    ),
    onClose,
  }
}

function renderEditModal({ entity = sampleProject, goals = sampleGoals, onClose = vi.fn() } = {}) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return {
    ...render(
      <QueryClientProvider client={client}>
        <ProjectModal entity={entity} goals={goals} onClose={onClose} />
      </QueryClientProvider>,
    ),
    onClose,
  }
}

beforeEach(() => {
  createMutateAsync.mockReset()
  updateMutateAsync.mockReset()
  useCreateProjectMock.mockReturnValue(makeMutation(createMutateAsync))
  useUpdateProjectMock.mockReturnValue(makeMutation(updateMutateAsync))
  useToastStore.setState({ toasts: [] })
})

afterEach(() => {
  vi.restoreAllMocks()
})

describe('ProjectModal — Create mode', () => {
  it('renders create title and focuses title input', () => {
    renderCreateModal()
    expect(screen.getByRole('heading', { level: 2 })).toHaveTextContent('Add Project')
    expect(document.activeElement).toBe(
      screen.getByLabelText(/^Title/i, { selector: 'input' }),
    )
  })

  it('shows required errors for title and slug on empty submit', async () => {
    const user = userEvent.setup()
    renderCreateModal()
    await user.click(screen.getByRole('button', { name: 'Add project' }))
    expect(screen.getByText('Title is required')).toBeInTheDocument()
    expect(screen.getByText('Slug is required')).toBeInTheDocument()
    expect(createMutateAsync).not.toHaveBeenCalled()
  })

  it('rejects an invalid slug with a clear error', async () => {
    const user = userEvent.setup()
    renderCreateModal()
    await user.type(screen.getByLabelText(/^Title/i, { selector: 'input' }), 'My project')
    await user.type(screen.getByLabelText(/Repo \/ slug name/i), 'Bad Slug')
    await user.click(screen.getByRole('button', { name: 'Add project' }))
    expect(
      screen.getByText('Use lowercase letters, digits, and single hyphens only'),
    ).toBeInTheDocument()
    expect(createMutateAsync).not.toHaveBeenCalled()
  })

  it('shows slug suggestion and applies it on click', async () => {
    const user = userEvent.setup()
    renderCreateModal()
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
    expect(
      screen.queryByRole('button', { name: /Suggested: my-cool-project/i }),
    ).toBeNull()
  })

  it('submits with trimmed payload + numeric priority + null goal_id', async () => {
    createMutateAsync.mockResolvedValue({})
    const user = userEvent.setup()
    const { onClose } = renderCreateModal({ goals: [] })
    await user.type(screen.getByLabelText(/^Title/i, { selector: 'input' }), 'Project Title')
    await user.type(screen.getByLabelText(/Repo \/ slug name/i), 'project-slug')
    await user.click(screen.getByRole('button', { name: 'Add project' }))
    await waitFor(() => expect(createMutateAsync).toHaveBeenCalledTimes(1))
    expect(createMutateAsync).toHaveBeenCalledWith({
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
    createMutateAsync.mockRejectedValue(new Error('409: conflict'))
    const user = userEvent.setup()
    renderCreateModal()
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
    renderCreateModal()
    const group = screen.getByRole('radiogroup', { name: 'Priority' })
    expect(group).toBeInTheDocument()
    const radios = screen.getAllByRole('radio')
    expect(radios).toHaveLength(5)
    expect(radios[2]).toHaveAttribute('aria-checked', 'true')
  })

  it('updates selected priority when clicking another level', async () => {
    const user = userEvent.setup()
    renderCreateModal()
    const radios = screen.getAllByRole('radio')
    await user.click(radios[4])
    expect(radios[4]).toHaveAttribute('aria-checked', 'true')
    expect(radios[2]).toHaveAttribute('aria-checked', 'false')
  })

  it('renders linked-goal select only when goals exist', () => {
    renderCreateModal({ goals: [] })
    expect(screen.queryByLabelText(/Linked goal/i)).toBeNull()
  })

  it('confirms before closing dirty form', async () => {
    const confirmSpy = vi.spyOn(window, 'confirm').mockReturnValue(false)
    const user = userEvent.setup()
    const { onClose } = renderCreateModal()
    await user.type(screen.getByLabelText(/^Title/i, { selector: 'input' }), 'X')
    await user.click(screen.getByRole('button', { name: 'Cancel' }))
    expect(confirmSpy).toHaveBeenCalled()
    expect(onClose).not.toHaveBeenCalled()
  })
})

describe('ProjectModal — Edit mode', () => {
  it('renders edit title and pre-fills fields from entity', () => {
    renderEditModal()
    expect(screen.getByRole('heading', { level: 2 })).toHaveTextContent('Edit Project')
    expect((screen.getByLabelText(/^Title/i, { selector: 'input' }) as HTMLInputElement).value).toBe('My Project')
    const nameInput = screen.getByLabelText(/Repo \/ slug name/i) as HTMLInputElement
    expect(nameInput.value).toBe('my-project')
  })

  it('slug field is disabled in Edit mode', () => {
    renderEditModal()
    const nameInput = screen.getByLabelText(/Repo \/ slug name/i) as HTMLInputElement
    expect(nameInput.disabled).toBe(true)
  })

  it('submit button is disabled when form is pristine in Edit mode', () => {
    renderEditModal()
    const submitBtn = screen.getByRole('button', { name: 'Save changes' })
    expect(submitBtn).toBeDisabled()
  })

  it('submit button becomes enabled after a field change', async () => {
    const user = userEvent.setup()
    renderEditModal()
    const titleInput = screen.getByLabelText(/^Title/i, { selector: 'input' })
    await user.clear(titleInput)
    await user.type(titleInput, 'Updated project name')
    expect(screen.getByRole('button', { name: 'Save changes' })).not.toBeDisabled()
  })

  it('calls useUpdateProject with entity id and updated fields', async () => {
    updateMutateAsync.mockResolvedValue({})
    const user = userEvent.setup()
    const { onClose } = renderEditModal()
    const titleInput = screen.getByLabelText(/^Title/i, { selector: 'input' })
    await user.clear(titleInput)
    await user.type(titleInput, 'Updated Project')
    await user.click(screen.getByRole('button', { name: 'Save changes' }))
    await waitFor(() => expect(updateMutateAsync).toHaveBeenCalledTimes(1))
    expect(updateMutateAsync).toHaveBeenCalledWith(
      expect.objectContaining({ id: 'proj-1', title: 'Updated Project' }),
    )
    expect(useToastStore.getState().toasts[0]?.message).toBe('Project updated')
    await waitFor(() => expect(onClose).toHaveBeenCalled())
  })

  it('does not call createProject in Edit mode', async () => {
    updateMutateAsync.mockResolvedValue({})
    const user = userEvent.setup()
    renderEditModal()
    const titleInput = screen.getByLabelText(/^Title/i, { selector: 'input' })
    await user.clear(titleInput)
    await user.type(titleInput, 'Changed')
    await user.click(screen.getByRole('button', { name: 'Save changes' }))
    await waitFor(() => expect(updateMutateAsync).toHaveBeenCalled())
    expect(createMutateAsync).not.toHaveBeenCalled()
  })

  it('shows save-failed error on update 5xx', async () => {
    updateMutateAsync.mockRejectedValue(new Error('500: server error'))
    const user = userEvent.setup()
    const { onClose } = renderEditModal()
    const titleInput = screen.getByLabelText(/^Title/i, { selector: 'input' })
    await user.clear(titleInput)
    await user.type(titleInput, 'Changed')
    await user.click(screen.getByRole('button', { name: 'Save changes' }))
    await waitFor(() => {
      expect(screen.getByText('Could not save. Try again.')).toBeInTheDocument()
    })
    expect(onClose).not.toHaveBeenCalled()
  })

  it('does not show slug suggestion in Edit mode', async () => {
    const user = userEvent.setup()
    renderEditModal()
    const titleInput = screen.getByLabelText(/^Title/i, { selector: 'input' })
    await user.clear(titleInput)
    await user.type(titleInput, 'A new title here')
    // No suggestion button should appear in edit mode
    expect(screen.queryByRole('button', { name: /Suggested:/i })).toBeNull()
  })

  it('renders status select with all ProjectStatus options', () => {
    renderEditModal()
    const select = screen.getByLabelText(/^Status/i, { selector: 'select' }) as HTMLSelectElement
    const options = Array.from(select.options).map((o) => o.value)
    expect(options).toEqual(['active', 'on_hold', 'completed', 'archived'])
  })
})
