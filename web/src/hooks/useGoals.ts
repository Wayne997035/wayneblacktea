import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { apiFetch } from '../lib/api'
import type { Goal, GoalStatus } from '../types/api'

export function useGoals() {
  return useQuery<Goal[]>({
    queryKey: ['goals'],
    queryFn: () => apiFetch<Goal[]>('/api/goals'),
    // Defence: backend may return JSON null for an empty list; never let a null reach .length.
    select: (data) => data ?? [],
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

export interface UpdateGoalRequest {
  title?: string;
  area?: string | null;
  description?: string | null;
  status?: GoalStatus;
  due_date?: string | null;
}

export function useUpdateGoal() {
  const queryClient = useQueryClient()
  return useMutation<Goal, Error, { id: string } & UpdateGoalRequest>({
    mutationFn: ({ id, ...body }) =>
      apiFetch<Goal>(`/api/goals/${id}`, {
        method: 'PATCH',
        body: JSON.stringify(body),
      }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['goals'] })
      void queryClient.invalidateQueries({ queryKey: ['context', 'today'] })
    },
  })
}
