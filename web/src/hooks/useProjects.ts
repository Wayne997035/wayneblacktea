import { useQuery } from '@tanstack/react-query'
import { apiFetch } from '../lib/api'
import type { Project } from '../types/api'

export function useProjects() {
  return useQuery<Project[]>({
    queryKey: ['projects'],
    queryFn: () => apiFetch<Project[]>('/api/projects'),
  })
}
