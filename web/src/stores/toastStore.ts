import { create } from 'zustand'

export interface Toast {
  id: string;
  message: string;
  type: 'error' | 'success' | 'info';
  duration?: number;
}

interface ToastState {
  toasts: Toast[]
  addToast: (toast: Omit<Toast, 'id'>) => void
  removeToast: (id: string) => void
}

export const useToastStore = create<ToastState>((set) => ({
  toasts: [],
  addToast: (toast) => {
    const id = Math.random().toString(36).slice(2)
    set((state) => ({
      toasts: [...state.toasts.slice(-2), { ...toast, id }],
    }))
  },
  removeToast: (id) =>
    set((state) => ({ toasts: state.toasts.filter((t) => t.id !== id) })),
}))
