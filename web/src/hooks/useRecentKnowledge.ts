import { useQuery } from '@tanstack/react-query'
import { apiFetch } from '../lib/api'
import type { KnowledgeItem } from '../types/api'

export function useRecentKnowledge() {
  return useQuery<KnowledgeItem[]>({
    queryKey: ['dashboard', 'recent-knowledge'],
    queryFn: () => apiFetch<KnowledgeItem[]>('/api/knowledge?limit=3'),
    staleTime: 120_000,
  })
}
