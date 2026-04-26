import { useTranslation } from 'react-i18next'
import type { ProjectStatus } from '../../types/api'

interface StatusBadgeProps {
  status: ProjectStatus;
  size?: 'sm' | 'md';
}

const statusStyles: Record<ProjectStatus, { bg: string; text: string }> = {
  active:    { bg: 'var(--color-status-active-bg)',    text: 'var(--color-status-active-text)' },
  on_hold:   { bg: 'var(--color-status-on-hold-bg)',   text: 'var(--color-status-on-hold-text)' },
  completed: { bg: 'var(--color-status-completed-bg)', text: 'var(--color-status-completed-text)' },
  archived:  { bg: 'var(--color-status-archived-bg)',  text: 'var(--color-status-archived-text)' },
}

export function StatusBadge({ status, size = 'md' }: StatusBadgeProps) {
  const { t } = useTranslation()
  const { bg, text } = statusStyles[status]
  const sizeClass = size === 'md'
    ? 'px-2 py-0.5 rounded-full text-label'
    : 'px-1.5 rounded-full font-mono font-medium'
  const fontSize = size === 'sm' ? '10px' : undefined

  return (
    <span
      className={`${sizeClass} font-mono whitespace-nowrap`}
      style={{ background: bg, color: text, fontSize }}
    >
      {t(`status.${status}`)}
    </span>
  )
}
