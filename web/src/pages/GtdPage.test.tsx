/**
 * Mutation-testable proof for the GtdPage consumption-point fix: the goal
 * progress card (GoalCard) undercounted completed tasks because
 * projectsQuery only asked for active projects, so a completed project's
 * tasks had no `project_id -> goal_id` mapping and silently dropped out of
 * `goalTaskCounts` (see GtdPage.tsx, GoalCard.tsx).
 *
 * `useProjectsMock` below faithfully mirrors the real backend contract
 * (status unset => active-only, status='all' => everything) documented in
 * internal/handler/gtd_handler.go's ListProjects. That means this test is
 * mutation-sensitive on its own: if GtdPage.tsx regresses `useProjects('all')`
 * back to `useProjects()`, the mock starts returning only the active project,
 * `p-done`'s tasks lose their goal mapping, and the assertion on the
 * rendered "3 / 5" count fails.
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { GtdPage } from './GtdPage'
import type { Goal, Project, Task } from '../types/api'

const useGoalsMock = vi.fn()
vi.mock('../hooks/useGoals', () => ({
  useGoals: () => useGoalsMock(),
}))

const useProjectsMock = vi.fn()
vi.mock('../hooks/useProjects', () => ({
  useProjects: (status?: string) => useProjectsMock(status),
}))

const useAllTasksMock = vi.fn()
const useTasksByProjectMock = vi.fn()
const useCompleteTaskMock = vi.fn()
vi.mock('../hooks/useTasks', () => ({
  useAllTasks: (status?: string) => useAllTasksMock(status),
  useTasksByProject: (projectId: string, status?: string) => useTasksByProjectMock(projectId, status),
  useCompleteTask: (projectId?: string) => useCompleteTaskMock(projectId),
}))

const goal: Goal = {
  id: 'g1',
  title: 'Ship the thing',
  status: 'active',
  created_at: '2026-04-01T00:00:00Z',
  updated_at: '2026-04-01T00:00:00Z',
}

function makeProject(overrides: Partial<Project>): Project {
  return {
    id: 'p?',
    name: 'demo',
    title: 'Demo',
    status: 'active',
    area: 'engineering',
    priority: 3,
    goal_id: 'g1',
    created_at: '2026-04-01T00:00:00Z',
    updated_at: '2026-05-01T00:00:00Z',
    ...overrides,
  }
}

function makeTask(overrides: Partial<Task>): Task {
  return {
    id: 't?',
    title: 'a task',
    status: 'pending',
    priority: 3,
    created_at: '2026-04-01T00:00:00Z',
    updated_at: '2026-05-01T00:00:00Z',
    ...overrides,
  }
}

// active project: 1 done, 1 open
const pActive = makeProject({ id: 'p-active', status: 'active' })
// completed project — excluded from the backend's default (active-only)
// response; only reachable via status=all.
const pDone = makeProject({ id: 'p-done', status: 'completed' })

const allProjects = [pActive, pDone]
const activeOnlyProjects = allProjects.filter((p) => p.status === 'active')

const allTasks: Task[] = [
  makeTask({ id: 't1', project_id: 'p-active', status: 'completed' }),
  makeTask({ id: 't2', project_id: 'p-active', status: 'pending' }),
  makeTask({ id: 't3', project_id: 'p-done', status: 'completed' }),
  makeTask({ id: 't4', project_id: 'p-done', status: 'completed' }),
  makeTask({ id: 't5', project_id: 'p-done', status: 'pending' }),
]

function renderPage() {
  return render(
    <MemoryRouter>
      <GtdPage />
    </MemoryRouter>,
  )
}

beforeEach(() => {
  useGoalsMock.mockReset()
  useProjectsMock.mockReset()
  useAllTasksMock.mockReset()
  useTasksByProjectMock.mockReset()
  useCompleteTaskMock.mockReset()

  useGoalsMock.mockReturnValue({ data: [goal], isLoading: false, isError: false })
  // Mirrors the real backend: status unset -> active only, 'all' -> everything.
  useProjectsMock.mockImplementation((status?: string) => ({
    data: status === 'all' ? allProjects : activeOnlyProjects,
    isLoading: false,
    isError: false,
  }))
  useAllTasksMock.mockReturnValue({ data: allTasks, isLoading: false, isError: false })
  useTasksByProjectMock.mockReturnValue({ data: [], isLoading: false, isError: false })
  useCompleteTaskMock.mockReturnValue({ mutateAsync: vi.fn() })
})

afterEach(() => {
  vi.restoreAllMocks()
})

describe('GtdPage goal progress rollup', () => {
  it("requests projects with status='all' so completed projects stay mapped to their goal", () => {
    renderPage()

    expect(useProjectsMock).toHaveBeenCalledWith('all')
  })

  it("counts tasks from a completed project into the goal's progress (3 / 5, not 1 / 2)", () => {
    renderPage()

    // Bug: with only the active project visible, p-done's 3 tasks (2 done,
    // 1 open) have no goal_id mapping and vanish -> card would read "1 / 2".
    // Fix: with status=all, both projects map to g1 -> "3 / 5".
    expect(screen.getByText('3 / 5')).toBeInTheDocument()
    expect(screen.queryByText('1 / 2')).not.toBeInTheDocument()
  })
})
