import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { apiFetch } from '../lib/api'
import { useToastStore } from '../stores/toastStore'
import type { VisionItem, VisionItemSummary, CreateVisionRequest, UpdateVisionRequest, VisionStatus } from '../types/api'

export function useVision(status?: VisionStatus) {
  const params = status ? `?status=${encodeURIComponent(status)}` : ''
  return useQuery<VisionItemSummary[]>({
    queryKey: ['vision', status ?? 'all'],
    queryFn: () => apiFetch<VisionItemSummary[]>(`/api/vision${params}`),
    // Defence: backend may return JSON null for an empty list; never let a null reach .length.
    select: (data) => data ?? [],
  })
}

export function useCreateVision() {
  const queryClient = useQueryClient()
  const addToast = useToastStore((s) => s.addToast)
  return useMutation<VisionItem, Error, CreateVisionRequest>({
    mutationFn: (data) =>
      apiFetch<VisionItem>('/api/vision', {
        method: 'POST',
        body: JSON.stringify(data),
      }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['vision'] })
    },
    onError: (err) => {
      addToast({ type: 'error', message: err.message ?? 'Failed to create vision item' })
    },
  })
}

export function useUpdateVision() {
  const queryClient = useQueryClient()
  const addToast = useToastStore((s) => s.addToast)
  return useMutation<VisionItem, Error, { id: string; data: UpdateVisionRequest }>({
    mutationFn: ({ id, data }) =>
      apiFetch<VisionItem>(`/api/vision/${encodeURIComponent(id)}`, {
        method: 'PATCH',
        body: JSON.stringify(data),
      }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['vision'] })
    },
    onError: (err) => {
      addToast({ type: 'error', message: err.message ?? 'Failed to update vision item' })
    },
  })
}
