import { useTranslation } from 'react-i18next'

interface QuickStatsProps {
  pendingTasks: number | null;
  decisionsToday: number | null;
}

interface StatCellProps {
  label: string;
  value: number | null;
}

function StatCell({ label, value }: StatCellProps) {
  return (
    <div
      className="flex-1 rounded-lg p-4"
      style={{
        background: 'var(--color-bg-card)',
        border: '1px solid var(--color-border)',
      }}
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

export function QuickStats({ pendingTasks, decisionsToday }: QuickStatsProps) {
  const { t } = useTranslation()
  return (
    <div className="flex gap-3">
      <StatCell label={t('dashboard.sections.pendingTasks')} value={pendingTasks} />
      <StatCell label={t('dashboard.sections.decisionsToday')} value={decisionsToday} />
    </div>
  )
}
