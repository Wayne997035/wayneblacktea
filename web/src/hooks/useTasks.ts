import { useQuery, useQueries, useMutation, useQueryClient } from '@tanstack/react-query'
import { apiFetch } from '../lib/api'
import type { Task, CreateTaskRequest, Project } from '../types/api'

export function useTasksByProject(projectId: string) {
  return useQuery<Task[]>({
    queryKey: ['projects', projectId, 'tasks'],
    queryFn: () => apiFetch<Task[]>(`/api/projects/${projectId}/tasks`),
    enabled: Boolean(projectId),
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
  return useMutation<Task, Error, string>({
    mutationFn: (taskId) =>
      apiFetch<Task>(`/api/tasks/${taskId}/complete`, { method: 'PATCH' }),
    onSuccess: () => {
      if (projectId) {
        void queryClient.invalidateQueries({ queryKey: ['projects', projectId, 'tasks'] })
      }
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
    },
  })
}
