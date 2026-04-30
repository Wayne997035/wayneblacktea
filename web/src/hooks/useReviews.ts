import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { apiFetch } from '../lib/api'
import type { DueReview, SubmitReviewRequest, CreateConceptRequest, LearningSuggestions } from '../types/api'

export function useNewConcepts() {
  return useQuery<DueReview[]>({
    queryKey: ['reviews', 'new'],
    queryFn: async () => {
      const all = await apiFetch<DueReview[]>('/api/learning/reviews?limit=100')
      return all.filter((r) => r.review_count === 0)
    },
  })
}

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

export function useLearningSuggestions() {
  return useQuery<LearningSuggestions>({
    queryKey: ['learning-suggestions'],
    queryFn: () => apiFetch<LearningSuggestions>('/api/learning/suggestions'),
    staleTime: 5 * 60 * 1000,
  })
}

export function useCreateConceptFromKnowledge() {
  const queryClient = useQueryClient()
  return useMutation<unknown, Error, { knowledge_id: string }>({
    mutationFn: (body) =>
      apiFetch('/api/learning/from-knowledge', {
        method: 'POST',
        body: JSON.stringify(body),
      }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['reviews'] })
      void queryClient.invalidateQueries({ queryKey: ['learning-suggestions'] })
    },
  })
}
