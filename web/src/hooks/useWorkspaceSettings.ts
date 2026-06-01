import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { apiFetch } from '../lib/api'
import type { WorkspaceSettings } from '../types/api'

export function useWorkspaceSettings() {
  return useQuery<WorkspaceSettings>({
    queryKey: ['workspace', 'settings'],
    queryFn: () => apiFetch<WorkspaceSettings>('/api/workspace/settings'),
  })
}

export function useUpdateWorkspaceSettings() {
  const queryClient = useQueryClient()
  return useMutation<WorkspaceSettings, Error, WorkspaceSettings>({
    mutationFn: (data) =>
      apiFetch<WorkspaceSettings>('/api/workspace/settings', {
        method: 'PATCH',
        body: JSON.stringify(data),
      }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['workspace', 'settings'] })
    },
  })
}
