import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { apiFetch } from '../lib/api'
import { useToastStore } from '../stores/toastStore'
import type { VisionItemSummary, CreateVisionRequest, UpdateVisionRequest, VisionStatus } from '../types/api'

export function useVision(status?: VisionStatus) {
  const params = status ? `?status=${encodeURIComponent(status)}` : ''
  return useQuery<VisionItemSummary[]>({
    queryKey: ['vision', status ?? 'all'],
    queryFn: () => apiFetch<VisionItemSummary[]>(`/api/vision${params}`),
  })
}

export function useCreateVision() {
  const queryClient = useQueryClient()
  const addToast = useToastStore((s) => s.addToast)
  return useMutation<VisionItemSummary, Error, CreateVisionRequest>({
    mutationFn: (data) =>
      apiFetch<VisionItemSummary>('/api/vision', {
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
  return useMutation<VisionItemSummary, Error, { id: string; data: UpdateVisionRequest }>({
    mutationFn: ({ id, data }) =>
      apiFetch<VisionItemSummary>(`/api/vision/${id}`, {
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
