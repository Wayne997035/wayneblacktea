import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { apiFetch } from '../lib/api'
import type { KnowledgeItem, CreateKnowledgeRequest } from '../types/api'

export function useKnowledge() {
  return useQuery<KnowledgeItem[]>({
    queryKey: ['knowledge'],
    queryFn: () => apiFetch<KnowledgeItem[]>('/api/knowledge?limit=20'),
  })
}

export function useKnowledgeSearch(query: string) {
  return useQuery<KnowledgeItem[]>({
    queryKey: ['knowledge', 'search', query],
    queryFn: () => apiFetch<KnowledgeItem[]>(`/api/knowledge/search?q=${encodeURIComponent(query)}&limit=10`),
    enabled: query.trim().length > 0,
  })
}

export function useCreateKnowledge() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (body: CreateKnowledgeRequest) =>
      apiFetch<KnowledgeItem>('/api/knowledge', {
        method: 'POST',
        body: JSON.stringify(body),
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['knowledge'] })
    },
  })
}

export interface UpdateKnowledgeRequest {
  learning_value?: number | null
}

export function useUpdateKnowledge() {
  const queryClient = useQueryClient()
  return useMutation<KnowledgeItem, Error, { id: string } & UpdateKnowledgeRequest>({
    mutationFn: ({ id, ...body }) =>
      apiFetch<KnowledgeItem>(`/api/knowledge/${id}`, {
        method: 'PATCH',
        body: JSON.stringify(body),
      }),
    onMutate: async ({ id, learning_value }) => {
      // Cancel any outgoing refetches so they don't overwrite our optimistic update
      await queryClient.cancelQueries({ queryKey: ['knowledge'] })
      const previousItems = queryClient.getQueryData<KnowledgeItem[]>(['knowledge'])
      // Optimistically update the list
      queryClient.setQueryData<KnowledgeItem[]>(['knowledge'], (old) =>
        old?.map((item) =>
          item.id === id
            ? { ...item, learning_value: learning_value ?? null }
            : item,
        ) ?? [],
      )
      return { previousItems }
    },
    onError: (_err, _vars, context) => {
      // Rollback on error
      const ctx = context as { previousItems?: KnowledgeItem[] } | undefined
      if (ctx?.previousItems) {
        queryClient.setQueryData(['knowledge'], ctx.previousItems)
      }
    },
    onSettled: () => {
      void queryClient.invalidateQueries({ queryKey: ['knowledge'] })
    },
  })
}
