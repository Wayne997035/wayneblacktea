import { useTranslation } from 'react-i18next'

interface QuickStatsProps {
  pendingTasks: number | null;
  decisionsToday: number | null;
  onPendingTasksClick?: () => void;
}

interface StatCellProps {
  label: string;
  value: number | null;
  onClick?: () => void;
}

function StatCell({ label, value, onClick }: StatCellProps) {
  const isInteractive = Boolean(onClick)

  return (
    <div
      role={isInteractive ? 'button' : undefined}
      tabIndex={isInteractive ? 0 : undefined}
      onClick={onClick}
      onKeyDown={
        onClick
          ? (e) => {
              if (e.key === 'Enter' || e.key === ' ') {
                e.preventDefault()
                onClick()
              }
            }
          : undefined
      }
      className="flex-1 rounded-lg p-4 transition-colors"
      style={{
        background: 'var(--color-bg-card)',
        border: '1px solid var(--color-border)',
        cursor: isInteractive ? 'pointer' : 'default',
      }}
      onMouseEnter={
        isInteractive
          ? (e) => {
              e.currentTarget.style.background = 'var(--color-bg-hover)'
            }
          : undefined
      }
      onMouseLeave={
        isInteractive
          ? (e) => {
              e.currentTarget.style.background = 'var(--color-bg-card)'
            }
          : undefined
      }
    >
      <div
        className="text-2xl font-semibold font-mono"
        style={{ color: 'var(--color-accent-blue)' }}
      >
        {value !== null ? value : '—'}
      </div>
      <div className="text-caption mt-1" style={{ color: 'var(--color-text-muted)' }}>
        {label}
      </div>
    </div>
  )
}

export function QuickStats({ pendingTasks, decisionsToday, onPendingTasksClick }: QuickStatsProps) {
  const { t } = useTranslation()
  return (
    <div className="flex gap-3">
      <StatCell
        label={t('dashboard.sections.pendingTasks')}
        value={pendingTasks}
        onClick={onPendingTasksClick}
      />
      <StatCell label={t('dashboard.sections.decisionsToday')} value={decisionsToday} />
    </div>
  )
}
