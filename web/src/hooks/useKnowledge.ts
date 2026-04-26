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
