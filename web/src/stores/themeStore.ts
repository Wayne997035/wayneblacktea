import { create } from 'zustand'

interface ThemeState {
  theme: 'dark' | 'light'
  toggle: () => void
}

export const useThemeStore = create<ThemeState>((set, get) => ({
  theme: (localStorage.getItem('wbt-theme') as 'dark' | 'light' | null) === 'light' ? 'light' : 'dark',
  toggle: () => {
    const next = get().theme === 'dark' ? 'light' : 'dark'
    localStorage.setItem('wbt-theme', next)
    if (next === 'light') {
      document.documentElement.classList.add('light')
    } else {
      document.documentElement.classList.remove('light')
    }
    set({ theme: next })
  },
}))
