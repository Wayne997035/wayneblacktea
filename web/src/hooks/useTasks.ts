import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { apiFetch } from '../lib/api'
import type { Task, CreateTaskRequest } from '../types/api'

export function useTasksByProject(projectId: string) {
  return useQuery<Task[]>({
    queryKey: ['projects', projectId, 'tasks'],
    queryFn: () => apiFetch<Task[]>(`/api/projects/${projectId}/tasks`),
    enabled: Boolean(projectId),
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
