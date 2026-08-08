import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { apiFetch } from '../lib/api'
import type { DueReview, SubmitReviewRequest, CreateConceptRequest, LearningSuggestions } from '../types/api'

export function useNewConcepts() {
  return useQuery<DueReview[]>({
    queryKey: ['reviews', 'new'],
    queryFn: async () => {
      // Guard before .filter: backend may return JSON null for an empty list,
      // and null.filter() would throw before the `select` defence below ever runs.
      const all = await apiFetch<DueReview[]>('/api/learning/reviews?limit=100')
      return (all ?? []).filter((r) => r.review_count === 0)
    },
    // Defence: never let a null reach .length (queryFn above already guarantees an
    // array, but keep this consistent with every other list hook).
    select: (data) => data ?? [],
  })
}

export function useReviews() {
  return useQuery<DueReview[]>({
    queryKey: ['reviews'],
    queryFn: () => apiFetch<DueReview[]>('/api/learning/reviews?limit=50'),
    // Defence: backend may return JSON null for an empty list; never let a null reach .length.
    select: (data) => data ?? [],
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
