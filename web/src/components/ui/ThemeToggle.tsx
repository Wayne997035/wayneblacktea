import { Moon, Sun } from 'lucide-react'
import { useThemeStore } from '../../stores/themeStore'

export function ThemeToggle() {
  const { theme, toggle } = useThemeStore()
  const isDark = theme === 'dark'

  return (
    <button
      type="button"
      onClick={toggle}
      aria-label="切換深色/淺色模式 / Toggle theme"
      aria-pressed={isDark}
      className="flex items-center justify-center w-11 h-11 rounded-md transition-colors"
      style={{ color: isDark ? 'var(--color-text-muted)' : 'var(--color-warning)' }}
      onMouseEnter={(e) => { e.currentTarget.style.background = 'var(--color-bg-hover)' }}
      onMouseLeave={(e) => { e.currentTarget.style.background = 'transparent' }}
    >
      {isDark ? <Moon size={18} aria-hidden="true" /> : <Sun size={18} aria-hidden="true" />}
    </button>
  )
}
