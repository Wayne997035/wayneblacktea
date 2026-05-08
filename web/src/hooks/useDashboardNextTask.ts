import { useQuery } from '@tanstack/react-query'
import { apiFetch } from '../lib/api'

export interface NextTask {
  id: string
  title: string
  priority: number | null
  status: string
  due_date?: string | null
}

export interface NextTaskResponse {
  task: NextTask | null
}

export function useDashboardNextTask() {
  return useQuery<NextTaskResponse>({
    queryKey: ['dashboard', 'next-task'],
    queryFn: () => apiFetch<NextTaskResponse>('/api/dashboard/next-task'),
    staleTime: 60_000,
  })
}
