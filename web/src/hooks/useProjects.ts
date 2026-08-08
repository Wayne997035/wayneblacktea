import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { apiFetch } from '../lib/api'
import type { Project, ProjectStatus } from '../types/api'

export function useProjects() {
  return useQuery<Project[]>({
    queryKey: ['projects'],
    queryFn: () => apiFetch<Project[]>('/api/projects'),
    // Defence: backend may return JSON null for an empty list; never let a null reach .length.
    select: (data) => data ?? [],
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

export interface UpdateProjectRequest {
  title?: string;
  area?: string | null;
  description?: string | null;
  status?: ProjectStatus;
  priority?: 1 | 2 | 3 | 4 | 5;
  goal_id?: string | null;
  // name (slug) intentionally omitted — slug is identity, immutable post-create
}

export function useUpdateProject() {
  const queryClient = useQueryClient()
  return useMutation<Project, Error, { id: string } & UpdateProjectRequest>({
    mutationFn: ({ id, ...body }) =>
      apiFetch<Project>(`/api/projects/${id}`, {
        method: 'PATCH',
        body: JSON.stringify(body),
      }),
    onSuccess: (_data, { id }) => {
      void queryClient.invalidateQueries({ queryKey: ['projects'] })
      void queryClient.invalidateQueries({ queryKey: ['projects', id] })
      void queryClient.invalidateQueries({ queryKey: ['context', 'today'] })
    },
  })
}
