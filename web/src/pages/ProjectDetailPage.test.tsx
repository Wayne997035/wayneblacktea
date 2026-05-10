import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter, Routes, Route } from 'react-router-dom'
import { ProjectDetailPage } from './ProjectDetailPage'
import type { Project, Task, TaskStatus } from '../types/api'

// useTasks: capture the (projectId, statusFilter) call signature so we can
// verify the page asks for ?status=all (the bug was that it only asked for
// active tasks, hiding the entire completed section).
const useTasksByProjectMock = vi.fn()
const useCompleteTaskMock = vi.fn()

vi.mock('../hooks/useTasks', () => ({
  useTasksByProject: (projectId: string, statusFilter: 'active' | 'all' = 'active') =>
    useTasksByProjectMock(projectId, statusFilter),
  useCompleteTask: (projectId?: string) => useCompleteTaskMock(projectId),
}))

const useProjectMock = vi.fn()
vi.mock('../hooks/useProjects', () => ({
  useProject: (id: string) => useProjectMock(id),
}))

const useDecisionsMock = vi.fn()
vi.mock('../hooks/useDecisions', () => ({
  useDecisions: (projectId: string, opts?: { enabled?: boolean }) =>
    useDecisionsMock(projectId, opts),
}))

const sampleProject: Project = {
  id: 'p1',
  name: 'demo',
  title: 'Demo Project',
  status: 'active',
  area: 'engineering',
  priority: 3,
  created_at: '2026-04-01T00:00:00Z',
  updated_at: '2026-05-01T00:00:00Z',
}

function makeTask(overrides: Partial<Task>): Task {
  return {
    id: 't?',
    title: 'a task',
    status: 'pending' as TaskStatus,
    priority: 3,
    created_at: '2026-04-01T00:00:00Z',
    updated_at: '2026-05-01T00:00:00Z',
    ...overrides,
  }
}

function renderPage(projectId = 'p1') {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={[`/projects/${projectId}`]}>
        <Routes>
          <Route path="/projects/:id" element={<ProjectDetailPage />} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  useTasksByProjectMock.mockReset()
  useCompleteTaskMock.mockReset()
  useProjectMock.mockReset()
  useDecisionsMock.mockReset()

  useProjectMock.mockReturnValue({ data: sampleProject, isLoading: false, isError: false })
  useDecisionsMock.mockReturnValue({ data: [], isLoading: false, isError: false })
  useCompleteTaskMock.mockReturnValue({ mutateAsync: vi.fn() })
})

afterEach(() => {
  vi.restoreAllMocks()
})

describe('ProjectDetailPage', () => {
  it('requests tasks with statusFilter="all" so completed rows are reachable', () => {
    useTasksByProjectMock.mockReturnValue({ data: [], isLoading: false })

    renderPage('p1')

    expect(useTasksByProjectMock).toHaveBeenCalledWith('p1', 'all')
  })

  it('renders the COMPLETED section with task title and timestamp when API returns done tasks', () => {
    const open = makeTask({ id: 't-open', title: 'open work', status: 'pending' })
    const done = makeTask({
      id: 't-done',
      title: 'shipped work',
      status: 'completed',
      updated_at: '2026-05-07T13:30:00Z',
    })
    useTasksByProjectMock.mockReturnValue({ data: [open, done], isLoading: false })

    renderPage()

    // Section header — the bug pre-fix: section was rendered but was always empty.
    expect(screen.getByText('COMPLETED')).toBeInTheDocument()
    // Count chip — exactly 1 done task in fixture.
    expect(screen.getByText('1 done')).toBeInTheDocument()
    // Completed task body — was previously filtered out by the backend.
    expect(screen.getByText('shipped work')).toBeInTheDocument()
    // Timestamp footer — sourced from updated_at; verify the raw value is
    // surfaced via data-completed-at so the rendered text doesn't lock the
    // assertion to a specific locale formatter.
    const footer = screen.getByTestId('completed-at')
    expect(footer).toHaveAttribute('data-completed-at', '2026-05-07T13:30:00Z')

    // The completed task MUST NOT also appear in the open list, and the
    // open task MUST NOT appear under "completed". Sanity check by looking
    // at the dedicated lists.
    const openList = screen.getByLabelText('Project open tasks')
    expect(openList).toHaveTextContent('open work')
    expect(openList).not.toHaveTextContent('shipped work')

    const doneList = screen.getByLabelText('Project completed tasks')
    expect(doneList).toHaveTextContent('shipped work')
    expect(doneList).not.toHaveTextContent('open work')
  })

  it('hides the COMPLETED section entirely when the project has no done tasks', () => {
    const open = makeTask({ id: 't-open', title: 'still going', status: 'pending' })
    useTasksByProjectMock.mockReturnValue({ data: [open], isLoading: false })

    renderPage()

    expect(screen.queryByText('COMPLETED')).toBeNull()
    expect(screen.queryByLabelText('Project completed tasks')).toBeNull()
    // Open list still rendered — guards against the new structure regressing
    // the empty-completed case to "no tasks at all".
    expect(screen.getByLabelText('Project open tasks')).toHaveTextContent('still going')
  })

  it('shows "no tasks yet" copy when API returns an empty list (neither section rendered)', () => {
    useTasksByProjectMock.mockReturnValue({ data: [], isLoading: false })

    renderPage()

    expect(screen.getByText('No tasks yet')).toBeInTheDocument()
    expect(screen.queryByText('COMPLETED')).toBeNull()
    expect(screen.queryByLabelText('Project open tasks')).toBeNull()
    expect(screen.queryByLabelText('Project completed tasks')).toBeNull()
  })
})
