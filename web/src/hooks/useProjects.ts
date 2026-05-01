import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { apiFetch } from '../lib/api'
import type { Project } from '../types/api'

export function useProjects() {
  return useQuery<Project[]>({
    queryKey: ['projects'],
    queryFn: () => apiFetch<Project[]>('/api/projects'),
  })
}

export function useProject(id: string) {
  return useQuery<Project>({
    queryKey: ['projects', id],
    queryFn: () => apiFetch<Project>(`/api/projects/${id}`),
    enabled: Boolean(id),
  })
}

export interface CreateProjectRequest {
  name: string;
  title: string;
  area?: string;
  description?: string;
  goal_id?: string | null;
  priority?: 1 | 2 | 3 | 4 | 5;
}

export function useCreateProject() {
  const queryClient = useQueryClient()
  return useMutation<Project, Error, CreateProjectRequest>({
    mutationFn: (data) =>
      apiFetch<Project>('/api/projects', {
        method: 'POST',
        body: JSON.stringify(data),
      }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['projects'] })
      void queryClient.invalidateQueries({ queryKey: ['context', 'today'] })
    },
  })
}
