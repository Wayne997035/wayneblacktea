import { X, Menu } from 'lucide-react'
import { ThemeToggle } from '../ui/ThemeToggle'
import { LanguageToggle } from '../ui/LanguageToggle'
import { useApiPing } from '../../hooks/useApiPing'
import { useTranslation } from 'react-i18next'

interface HeaderProps {
  onMenuClick?: () => void;
  sidebarOpen?: boolean;
}

export function Header({ onMenuClick, sidebarOpen = false }: HeaderProps) {
  const { t } = useTranslation()
  const { isSuccess, isError } = useApiPing()
  const apiConnected = isSuccess && !isError

  return (
    <header
      className="flex items-center justify-between px-3 sm:px-4 sticky top-0 z-[60] shrink-0"
      style={{
        height: 'var(--spacing-header)',
        background: 'var(--color-bg-card)',
        borderBottom: '1px solid var(--color-border)',
      }}
    >
      <div className="flex items-center gap-2 sm:gap-3 min-w-0">
        {/* Hamburger: visible on mobile + tablet (< lg), hidden on desktop */}
        <button
          type="button"
          onClick={onMenuClick}
          aria-label={sidebarOpen ? 'Close navigation menu' : 'Open navigation menu'}
          aria-expanded={sidebarOpen}
          className="lg:hidden flex items-center justify-center w-10 h-10 rounded-md transition-colors"
          style={{ background: 'transparent' }}
          onMouseEnter={(e) => { e.currentTarget.style.background = 'var(--color-bg-hover)' }}
          onMouseLeave={(e) => { e.currentTarget.style.background = 'transparent' }}
        >
          <span
            style={{
              display: 'block',
              transition: 'transform 220ms ease, opacity 220ms ease',
              transform: sidebarOpen ? 'rotate(90deg)' : 'rotate(0deg)',
            }}
          >
            {sidebarOpen
              ? <X size={20} aria-hidden="true" />
              : <Menu size={20} aria-hidden="true" />
            }
          </span>
        </button>

        <a
          href="/"
          className="flex items-center gap-2 rounded outline-none focus-visible:ring-2 min-w-0"
          aria-label="wayneblacktea — home"
          style={{ textDecoration: 'none' }}
        >
          <img
            src="/icon.png"
            alt=""
            aria-hidden="true"
            width={72}
            height={48}
            className="h-10 w-auto shrink-0 sm:h-12"
            style={{ display: 'block', objectFit: 'contain' }}
          />
          <span
            className="hidden sm:inline font-mono font-medium tracking-widest text-sm"
            style={{ color: 'var(--color-accent-blue)' }}
          >
            CONTROL ROOM
          </span>
        </a>
      </div>

      <div className="flex items-center gap-1 shrink-0">
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
