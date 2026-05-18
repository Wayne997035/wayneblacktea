import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { apiFetch } from '../lib/api'
import type { VisionItem, CreateVisionRequest, UpdateVisionRequest } from '../types/api'

export function useVision() {
  return useQuery<VisionItem[]>({
    queryKey: ['vision'],
    queryFn: () => apiFetch<VisionItem[]>('/api/vision'),
  })
}

export function useCreateVision() {
  const queryClient = useQueryClient()
  return useMutation<VisionItem, Error, CreateVisionRequest>({
    mutationFn: (data) =>
      apiFetch<VisionItem>('/api/vision', {
        method: 'POST',
        body: JSON.stringify(data),
      }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['vision'] })
    },
    onError: (error: Error) => {
      console.error('useCreateVision: create failed', error)
    },
  })
}

export function useUpdateVision() {
  const queryClient = useQueryClient()
  return useMutation<VisionItem, Error, { id: string } & UpdateVisionRequest>({
    mutationFn: ({ id, ...data }) =>
      apiFetch<VisionItem>(`/api/vision/${id}`, {
        method: 'PATCH',
        body: JSON.stringify(data),
      }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['vision'] })
    },
    onError: (error: Error) => {
      console.error('useUpdateVision: update failed', error)
    },
  })
}
