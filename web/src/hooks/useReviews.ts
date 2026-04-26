import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { apiFetch } from '../lib/api'
import type { DueReview, SubmitReviewRequest, CreateConceptRequest } from '../types/api'

export function useReviews() {
  return useQuery<DueReview[]>({
    queryKey: ['reviews'],
    queryFn: () => apiFetch<DueReview[]>('/api/learning/reviews?limit=50'),
  })
}

export function useSubmitReview() {
  const queryClient = useQueryClient()
  return useMutation<{ status: string }, Error, { scheduleId: string } & SubmitReviewRequest>({
    mutationFn: ({ scheduleId, ...body }) =>
      apiFetch<{ status: string }>(`/api/learning/reviews/${scheduleId}/submit`, {
        method: 'POST',
        body: JSON.stringify(body),
      }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['reviews'] })
    },
  })
}

export function useCreateConcept() {
  const queryClient = useQueryClient()
  return useMutation<unknown, Error, CreateConceptRequest>({
    mutationFn: (body) =>
      apiFetch('/api/learning/concepts', {
        method: 'POST',
        body: JSON.stringify(body),
      }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['reviews'] })
    },
  })
}
