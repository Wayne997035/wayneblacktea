import { useQuery } from '@tanstack/react-query'
import { apiFetch } from '../lib/api'

export interface UpcomingTask {
  id: string
  title: string
  due_date: string
  status: string
  priority: number
}

export function useDashboardUpcoming() {
  return useQuery<UpcomingTask[]>({
    queryKey: ['dashboard', 'upcoming'],
    queryFn: () => apiFetch<UpcomingTask[]>('/api/dashboard/upcoming'),
    staleTime: 30_000,
  })
}
