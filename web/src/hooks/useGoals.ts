import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { apiFetch } from '../lib/api'
import type { Goal } from '../types/api'

export function useGoals() {
  return useQuery<Goal[]>({
    queryKey: ['goals'],
    queryFn: () => apiFetch<Goal[]>('/api/goals'),
  })
}

export interface CreateGoalRequest {
  title: string;
  area?: string;
  description?: string;
  due_date?: string | null;
}

export function useCreateGoal() {
  const queryClient = useQueryClient()
  return useMutation<Goal, Error, CreateGoalRequest>({
    mutationFn: (data) =>
      apiFetch<Goal>('/api/goals', {
        method: 'POST',
        body: JSON.stringify(data),
      }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['goals'] })
      void queryClient.invalidateQueries({ queryKey: ['context', 'today'] })
    },
  })
}
