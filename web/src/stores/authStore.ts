import { create } from 'zustand'

type Status = 'loading' | 'authenticated' | 'unauthenticated'

type AuthStore = {
  status: Status
  setStatus: (s: Status) => void
}

export const useAuthStore = create<AuthStore>((set) => ({
  status: 'loading',
  setStatus: (status) => set({ status }),
}))
