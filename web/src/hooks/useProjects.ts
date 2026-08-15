import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { apiFetch } from '../lib/api'
import type { Project, ProjectStatus } from '../types/api'

/**
 * Status filter passed to GET /api/projects.
 *
 * - unset (default): backend returns active projects only. This is the
 *   historical contract used by GTD list pages — keep it as the default so
 *   existing callers don't regress.
 * - 'all': backend returns every status (active/on_hold/completed/archived).
 *   Needed by aggregations that must see every project regardless of status
 *   (e.g. the GTD goal-progress rollup) — otherwise tasks that belong to a
 *   completed project have no project to map back to and silently drop out
 *   of the count.
 */
export type ProjectStatusFilter = ProjectStatus | 'all'

export function useProjects(status?: ProjectStatusFilter) {
  const suffix = status ? `?status=${status}` : ''
  return useQuery<Project[]>({
    // Status is part of the cache key so the default fetch and the 'all'
    // fetch don't share a cache entry — otherwise whichever resolves last
    // would clobber the other's data (e.g. DecisionsPage's default call vs.
    // GtdPage's 'all' call racing on the same key).
    queryKey: status ? ['projects', status] : ['projects'],
    queryFn: () => apiFetch<Project[]>(`/api/projects${suffix}`),
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
