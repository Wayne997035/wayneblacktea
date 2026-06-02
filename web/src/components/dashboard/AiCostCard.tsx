import { useTranslation } from 'react-i18next'
import { useAiCost } from '../../hooks/useAiCost'
import { LoadingSkeleton } from '../ui/LoadingSkeleton'
import { EmptyState } from '../ui/EmptyState'
import type { AiCostRow } from '../../types/api'

function formatCost(usd: number): string {
  if (usd < 0.001) return '< $0.001'
  if (usd < 1) return `$${usd.toFixed(3)}`
  return `$${usd.toFixed(2)}`
}

function formatTokens(n: number): string {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`
  if (n >= 1_000) return `${(n / 1_000).toFixed(1)}K`
  return String(n)
}

interface ModelRowProps {
  row: AiCostRow
}

function ModelRow({ row }: ModelRowProps) {
  return (
    <div
      className="flex items-center justify-between py-2"
      style={{ borderTop: '1px solid var(--color-border)' }}
    >
      <div className="flex-1 min-w-0 mr-3">
        <span
          className="text-body-sm block truncate"
          style={{ color: 'var(--color-text-primary)' }}
          title={row.model}
        >
          {row.model}
        </span>
        <span className="text-caption" style={{ color: 'var(--color-text-muted)' }}>
          {formatTokens(row.input_tokens)} in · {formatTokens(row.output_tokens)} out
        </span>
      </div>
      <span
        className="text-body-sm font-mono shrink-0"
        style={{ color: 'var(--color-accent-blue)' }}
      >
        {formatCost(row.total_cost_usd)}
      </span>
    </div>
  )
}

export function AiCostCard() {
  const { t } = useTranslation()
  const { data, isLoading, isError } = useAiCost()

  if (isLoading) {
    return (
      <div
        className="rounded-lg p-4"
        style={{
          background: 'var(--color-bg-card)',
          border: '1px solid var(--color-border)',
        }}
      >
        <LoadingSkeleton style={{ width: '55%', height: '14px', marginBottom: '12px' }} />
        {Array.from({ length: 2 }, (_, i) => (
          <div
            key={i}
            className="flex items-center justify-between py-2"
            style={{ borderTop: '1px solid var(--color-border)' }}
          >
            <LoadingSkeleton style={{ width: `${50 + i * 10}%`, height: '12px' }} />
            <LoadingSkeleton style={{ width: '48px', height: '12px' }} />
          </div>
        ))}
      </div>
    )
  }

  if (isError) {
    return (
      <div
        className="rounded-lg p-4"
        style={{
          background: 'var(--color-bg-card)',
          border: '1px solid var(--color-border)',
        }}
      >
        <div className="text-label mb-2" style={{ color: 'var(--color-text-muted)' }}>
          {t('dashboard.aiCost.title')}
        </div>
        <EmptyState messageKey="error.loadFailed" />
      </div>
    )
  }

  const rows = data?.by_model ?? []
  const total = data?.total_cost_usd ?? 0
  const period = data?.period ?? '30d'

  return (
    <div
      className="rounded-lg"
      style={{
        background: 'var(--color-bg-card)',
        border: '1px solid var(--color-border)',
      }}
    >
      <div className="px-4 pt-4 pb-2 flex items-center justify-between">
        <span className="text-label" style={{ color: 'var(--color-text-muted)' }}>
          {t('dashboard.aiCost.title')}
        </span>
        <span className="text-caption" style={{ color: 'var(--color-text-muted)' }}>
          {period}
        </span>
      </div>

      {rows.length === 0 ? (
        <div style={{ borderTop: '1px solid var(--color-border)' }}>
          <EmptyState messageKey="dashboard.aiCost.empty" />
        </div>
      ) : (
        <div className="px-4 pb-3">
          {rows.map((row) => (
            <ModelRow key={row.model} row={row} />
          ))}
          <div
            className="flex items-center justify-between pt-2 mt-1"
            style={{ borderTop: '2px solid var(--color-border)' }}
          >
            <span className="text-body-sm font-medium" style={{ color: 'var(--color-text-muted)' }}>
              {t('dashboard.aiCost.total')}
            </span>
            <span
              className="text-body-sm font-mono font-semibold"
              style={{ color: 'var(--color-accent-blue)' }}
            >
              {formatCost(total)}
            </span>
          </div>
        </div>
      )}
    </div>
  )
}
