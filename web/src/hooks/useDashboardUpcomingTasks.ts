import { useQuery } from '@tanstack/react-query'
import { apiFetch } from '../lib/api'

export interface UpcomingTaskItem {
  id: string
  title: string
  status: string
  priority: number
  importance?: number | null
  due_date?: string | null
  project_id?: string | null
}

export interface UpcomingTaskGroups {
  today: UpcomingTaskItem[]
  tomorrow: UpcomingTaskItem[]
  day_after: UpcomingTaskItem[]
  upcoming: UpcomingTaskItem[]
  unscheduled_important: UpcomingTaskItem[]
}

export interface UpcomingTasksResponse {
  groups: UpcomingTaskGroups
}

export function useDashboardUpcomingTasks(days = 7) {
  const tz = Intl.DateTimeFormat().resolvedOptions().timeZone

  return useQuery<UpcomingTasksResponse>({
    queryKey: ['dashboard', 'upcoming-tasks', { days, tz }],
    queryFn: () =>
      apiFetch<UpcomingTasksResponse>(
        `/api/dashboard/upcoming-tasks?days=${days}&tz=${encodeURIComponent(tz)}`,
      ),
    staleTime: 60_000,
  })
}
