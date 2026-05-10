import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { DecisionCreateModal } from './DecisionCreateModal'
import { useToastStore } from '../../stores/toastStore'
import type { Project } from '../../types/api'

const mutateAsync = vi.fn()
const useLogDecisionMock = vi.fn()

vi.mock('../../hooks/useDecisions', () => ({
  useLogDecision: () => useLogDecisionMock(),
}))

function makeMutation(impl: typeof mutateAsync, isPending = false) {
  return { mutateAsync: impl, isPending }
}

const sampleProjects: Project[] = [
  {
    id: 'p1',
    name: 'p1',
    title: 'Project One',
    status: 'active',
    area: '',
    priority: 3,
    created_at: '',
    updated_at: '',
  },
]

function renderModal({
  projects = sampleProjects,
  initialProjectId,
  onClose = vi.fn(),
}: {
  projects?: Project[];
  initialProjectId?: string;
  onClose?: () => void;
} = {}) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return {
    ...render(
      <QueryClientProvider client={client}>
        <DecisionCreateModal
          projects={projects}
          initialProjectId={initialProjectId}
          onClose={onClose}
        />
      </QueryClientProvider>,
    ),
    onClose,
  }
}

beforeEach(() => {
  mutateAsync.mockReset()
  useLogDecisionMock.mockReturnValue(makeMutation(mutateAsync))
  useToastStore.setState({ toasts: [] })
})

afterEach(() => {
  vi.restoreAllMocks()
})

describe('DecisionCreateModal', () => {
  it('renders the create title and focuses the title input', () => {
    renderModal()
    expect(screen.getByRole('heading', { level: 2 })).toHaveTextContent('Log Decision')
    expect(document.activeElement).toBe(
      screen.getByLabelText(/^Title/i, { selector: 'input' }),
    )
  })

  it('shows required errors for all four required fields when empty', async () => {
    const user = userEvent.setup()
    renderModal()
    await user.click(screen.getByRole('button', { name: 'Log decision' }))
    expect(screen.getByText('Title is required')).toBeInTheDocument()
    expect(screen.getByText('Context is required')).toBeInTheDocument()
    expect(screen.getByText('Decision is required')).toBeInTheDocument()
    expect(screen.getByText('Rationale is required')).toBeInTheDocument()
    expect(mutateAsync).not.toHaveBeenCalled()
  })

  it('submits with full payload and emits success toast', async () => {
    mutateAsync.mockResolvedValue({})
    const user = userEvent.setup()
    const { onClose } = renderModal()
    await user.type(screen.getByLabelText(/^Title/i, { selector: 'input' }), 'Use SQLite locally')
    await user.type(screen.getByLabelText(/^Context/i), 'Need offline dev')
    await user.type(screen.getByLabelText(/^Decision/i, { selector: 'textarea' }), 'Adopt SQLite')
    await user.type(screen.getByLabelText(/^Rationale/i), 'Zero-setup, fast tests')
    await user.click(screen.getByRole('button', { name: 'Log decision' }))
    await waitFor(() => expect(mutateAsync).toHaveBeenCalled())
    expect(mutateAsync).toHaveBeenCalledWith({
      title: 'Use SQLite locally',
      context: 'Need offline dev',
      decision: 'Adopt SQLite',
      rationale: 'Zero-setup, fast tests',
      alternatives: undefined,
      repo_name: undefined,
      project_id: null,
    })
    expect(useToastStore.getState().toasts[0]?.message).toBe('Decision logged')
    await waitFor(() => expect(onClose).toHaveBeenCalled())
  })

  it('forwards initialProjectId to the project select', () => {
    renderModal({ initialProjectId: 'p1' })
    const select = screen.getByLabelText(/^Project/i) as HTMLSelectElement
    expect(select.value).toBe('p1')
  })

  it('renders char counter on context textarea', async () => {
    const user = userEvent.setup()
    renderModal()
    await user.type(screen.getByLabelText(/^Context/i), 'hi')
    expect(screen.getByText('2 / 4000')).toBeInTheDocument()
  })

  it('shows generic save-failed message on 5xx errors', async () => {
    mutateAsync.mockRejectedValue(new Error('500: server'))
    const user = userEvent.setup()
    const { onClose } = renderModal()
    await user.type(screen.getByLabelText(/^Title/i, { selector: 'input' }), 'T')
    await user.type(screen.getByLabelText(/^Context/i), 'C')
    await user.type(screen.getByLabelText(/^Decision/i, { selector: 'textarea' }), 'D')
    await user.type(screen.getByLabelText(/^Rationale/i), 'R')
    await user.click(screen.getByRole('button', { name: 'Log decision' }))
    await waitFor(() => {
      expect(screen.getByText('Could not save. Try again.')).toBeInTheDocument()
    })
    expect(onClose).not.toHaveBeenCalled()
  })

  it('confirms before close when form is dirty', async () => {
    const confirmSpy = vi.spyOn(window, 'confirm').mockReturnValue(false)
    const user = userEvent.setup()
    const { onClose } = renderModal()
    await user.type(screen.getByLabelText(/^Title/i, { selector: 'input' }), 'X')
    await user.click(screen.getByRole('button', { name: 'Cancel' }))
    expect(confirmSpy).toHaveBeenCalled()
    expect(onClose).not.toHaveBeenCalled()
  })
})
