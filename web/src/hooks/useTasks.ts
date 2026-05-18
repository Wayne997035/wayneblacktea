import { useQuery, useQueries, useMutation, useQueryClient } from '@tanstack/react-query'
import { apiFetch } from '../lib/api'
import { useToastStore } from '../stores/toastStore'
import type { Task, CreateTaskRequest, Project } from '../types/api'

/**
 * Filter passed to the project-tasks endpoint.
 *
 * - `'active'` (default): backend returns only `pending` / `in_progress`.
 *   This is the historical contract used by GTD list pages — keep it as the
 *   default so existing callers don't regress.
 * - `'all'`: backend returns every status (including `completed` /
 *   `cancelled`), ordered by `COALESCE(updated_at, created_at) DESC`. Used
 *   by the project-detail page so the "completed" section actually has rows.
 */
export type TaskStatusFilter = 'active' | 'all'

export function useTasksByProject(projectId: string, statusFilter: TaskStatusFilter = 'active') {
  const suffix = statusFilter === 'all' ? '?status=all' : ''
  return useQuery<Task[]>({
    // Filter is part of the cache key so 'all' and 'active' don't share state
    // (otherwise toggling between them would surface stale rows).
    queryKey: ['projects', projectId, 'tasks', statusFilter],
    queryFn: () => apiFetch<Task[]>(`/api/projects/${projectId}/tasks${suffix}`),
    enabled: Boolean(projectId),
  })
}

export function useAllTasks(statusFilter: TaskStatusFilter = 'active') {
  const suffix = statusFilter === 'all' ? '?status=all' : ''
  return useQuery<Task[]>({
    queryKey: ['tasks', statusFilter],
    queryFn: () => apiFetch<Task[]>(`/api/tasks${suffix}`),
  })
}

export function useTasksForAllProjects(projects: Project[]) {
  const queries = useQueries({
    queries: projects.map((p) => ({
      queryKey: ['projects', p.id, 'tasks'] as const,
      queryFn: () => apiFetch<Task[]>(`/api/projects/${p.id}/tasks`),
      enabled: Boolean(p.id),
    })),
  })

  const isLoading = queries.some((q) => q.isLoading)
  const tasks = queries.flatMap((q) => q.data ?? [])
  return { isLoading, tasks }
}

export function useCompleteTask(projectId?: string) {
  const queryClient = useQueryClient()
  const { addToast } = useToastStore()
  return useMutation<Task, Error, string>({
    mutationFn: (taskId) =>
      apiFetch<Task>(`/api/tasks/${taskId}/complete`, { method: 'PATCH' }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['tasks'] })
      if (projectId) {
        void queryClient.invalidateQueries({ queryKey: ['projects', projectId, 'tasks'] })
      }
      void queryClient.invalidateQueries({ queryKey: ['dashboard', 'upcoming'] })
      void queryClient.invalidateQueries({ queryKey: ['dashboard', 'stats'] })
      void queryClient.invalidateQueries({ queryKey: ['dashboard', 'automation-feed'] })
    },
    onError: (error: Error) => {
      console.error('useCompleteTask: complete failed', error)
      addToast({ type: 'error', message: 'Could not complete task. Try again.' })
    },
  })
}

export function useCreateTask() {
  const queryClient = useQueryClient()
  return useMutation<Task, Error, CreateTaskRequest>({
    mutationFn: (data) =>
      apiFetch<Task>('/api/tasks', {
        method: 'POST',
        body: JSON.stringify(data),
      }),
    onSuccess: (_, vars) => {
      if (vars.project_id) {
        void queryClient.invalidateQueries({
          queryKey: ['projects', vars.project_id, 'tasks'],
        })
      }
      void queryClient.invalidateQueries({ queryKey: ['projects'] })
      void queryClient.invalidateQueries({ queryKey: ['dashboard', 'upcoming'] })
      void queryClient.invalidateQueries({ queryKey: ['dashboard', 'stats'] })
      void queryClient.invalidateQueries({ queryKey: ['dashboard', 'automation-feed'] })
    },
  })
}
