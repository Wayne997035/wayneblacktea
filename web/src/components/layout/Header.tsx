import { Menu } from 'lucide-react'
import { ThemeToggle } from '../ui/ThemeToggle'
import { LanguageToggle } from '../ui/LanguageToggle'
import { useApiPing } from '../../hooks/useApiPing'
import { useTranslation } from 'react-i18next'

interface HeaderProps {
  onMenuClick?: () => void;
  showMenu?: boolean;
}

export function Header({ onMenuClick, showMenu = false }: HeaderProps) {
  const { t } = useTranslation()
  const { isSuccess, isError } = useApiPing()
  const apiConnected = isSuccess && !isError

  return (
    <header
      className="flex items-center justify-between px-4 sticky top-0 z-30 shrink-0"
      style={{
        height: '56px',
        background: 'var(--color-bg-card)',
        borderBottom: '1px solid var(--color-border)',
      }}
    >
      <div className="flex items-center gap-3">
        {showMenu && (
          <button
            type="button"
            onClick={onMenuClick}
            aria-label="Open navigation menu"
            className="flex items-center justify-center w-10 h-10 rounded-md transition-colors"
            onMouseEnter={(e) => { e.currentTarget.style.background = 'var(--color-bg-hover)' }}
            onMouseLeave={(e) => { e.currentTarget.style.background = 'transparent' }}
          >
            <Menu size={20} aria-hidden="true" />
          </button>
        )}
        <span
          className="font-mono font-medium tracking-widest text-sm"
          style={{ color: 'var(--color-accent-blue)' }}
        >
          CONTROL ROOM
        </span>
      </div>

      <div className="flex items-center gap-1">
        <LanguageToggle />
        <ThemeToggle />
        <div
          className="w-2 h-2 rounded-full ml-2"
          style={{ background: apiConnected ? 'var(--color-success)' : 'var(--color-error)' }}
          aria-label={apiConnected ? t('api.connected') : t('api.disconnected')}
          role="img"
          title={apiConnected ? t('api.connected') : t('api.disconnected')}
        />
      </div>
    </header>
  )
}
