import { NavLink } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import type { LucideIcon } from 'lucide-react'
import { Lock } from 'lucide-react'

interface NavItemProps {
  icon: LucideIcon;
  labelKey: string;
  to: string;
  phase?: 1 | 3;
  collapsed?: boolean;
  onNavigate?: () => void;
}

export function NavItem({ icon: Icon, labelKey, to, phase = 1, collapsed = false, onNavigate }: NavItemProps) {
  const { t } = useTranslation()
  const label = t(labelKey)
  const isComingSoon = phase === 3

  if (isComingSoon) {
    return (
      <span
        role="button"
        aria-disabled="true"
        className="flex items-center gap-3 px-4 py-3 rounded-md cursor-default select-none"
        style={{ opacity: 0.5 }}
      >
        <Icon size={20} aria-hidden="true" className="shrink-0" />
        {!collapsed && (
          <>
            <span className="text-body flex-1" style={{ color: 'var(--color-text-muted)' }}>{label}</span>
            <span
              className="text-label rounded-full px-2 py-0.5 shrink-0"
              style={{
                color: 'var(--color-warning)',
                background: '#2e1f00',
              }}
            >
              SOON
            </span>
            <Lock size={12} aria-hidden="true" style={{ color: 'var(--color-text-muted)' }} />
          </>
        )}
      </span>
    )
  }

  return (
    <NavLink
      to={to}
      onClick={onNavigate}
      className={({ isActive }) =>
        `flex items-center gap-3 px-4 py-3 rounded-md transition-colors relative group ${
          isActive
            ? 'border-l-[3px] text-body'
            : 'text-body'
        }`
      }
      style={({ isActive }) => ({
        background: isActive ? 'var(--color-bg-hover)' : 'transparent',
        color: isActive ? 'var(--color-text-primary)' : 'var(--color-text-muted)',
        borderLeftColor: isActive ? 'var(--color-accent-blue)' : 'transparent',
        borderLeftWidth: '3px',
        borderLeftStyle: 'solid',
      })}
      onMouseEnter={(e) => {
        const el = e.currentTarget
        if (!el.classList.contains('active')) {
          el.style.background = 'var(--color-bg-hover)'
          el.style.color = 'var(--color-text-primary)'
        }
      }}
      onMouseLeave={(e) => {
        const el = e.currentTarget
        if (!el.classList.contains('active')) {
          el.style.background = 'transparent'
          el.style.color = 'var(--color-text-muted)'
        }
      }}
    >
      <Icon size={20} aria-hidden="true" className="shrink-0" />
      {!collapsed && (
        <span className="text-body">{label}</span>
      )}
      {collapsed && (
        <span
          role="tooltip"
          className="absolute left-full ml-2 px-2 py-1 rounded text-body-sm whitespace-nowrap z-50 opacity-0 group-hover:opacity-100 pointer-events-none transition-opacity"
          style={{
            background: 'var(--color-bg-card)',
            border: '1px solid var(--color-border)',
            color: 'var(--color-text-primary)',
          }}
        >
          {label}
        </span>
      )}
    </NavLink>
  )
}
