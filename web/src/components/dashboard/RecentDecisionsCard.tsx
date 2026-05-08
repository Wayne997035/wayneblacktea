import { useId } from 'react'
import { LoadingSkeleton } from '../ui/LoadingSkeleton'
import { EmptyState } from '../ui/EmptyState'
import { useDashboardRecentDecisions } from '../../hooks/useDashboardRecentDecisions'
import { formatRelative } from '../../lib/formatRelative'

interface RecentDecisionsCardProps {
  onSeeAll: () => void
}

export function RecentDecisionsCard({ onSeeAll }: RecentDecisionsCardProps) {
  const headerId = useId()
  const { data, isLoading } = useDashboardRecentDecisions()

  if (isLoading) {
    return (
      <div
        className="rounded-lg"
        style={{
          background: 'var(--color-bg-card)',
          border: '1px solid var(--color-border)',
        }}
      >
        {/* Header */}
        <div className="flex items-center justify-between px-4 pt-4 pb-3">
          <span className="text-label" style={{ color: 'var(--color-text-muted)' }}>
            RECENT DECISIONS
          </span>
        </div>
        {/* Skeleton rows */}
        {Array.from({ length: 3 }, (_, i) => (
          <div
            key={i}
            className="px-4 py-3"
            style={{ borderTop: '1px solid var(--color-border)' }}
          >
            <div className="flex items-center justify-between gap-2 mb-1">
              <LoadingSkeleton style={{ width: '70%', height: '14px' }} />
              <LoadingSkeleton style={{ width: '40px', height: '14px' }} />
            </div>
            <LoadingSkeleton style={{ width: '40%', height: '10px' }} />
          </div>
        ))}
      </div>
    )
  }

  const decisions = data ?? []

  return (
    <section
      aria-labelledby={headerId}
      className="rounded-lg"
      style={{
        background: 'var(--color-bg-card)',
        border: '1px solid var(--color-border)',
      }}
    >
      {/* Header */}
      <div className="flex items-center justify-between px-4 pt-4 pb-3">
        <span
          id={headerId}
          className="text-label"
          style={{ color: 'var(--color-text-muted)' }}
        >
          RECENT DECISIONS
        </span>
        {decisions.length > 0 && (
          <button
            type="button"
            aria-label="See all decisions"
            className="text-caption rounded px-2 py-1 transition-colors"
            style={{ color: 'var(--color-accent-blue)', background: 'transparent', border: 'none', cursor: 'pointer' }}
            onClick={onSeeAll}
            onMouseEnter={(e) => {
              e.currentTarget.style.color = 'var(--color-text-primary)'
              e.currentTarget.style.background = 'var(--color-bg-hover)'
            }}
            onMouseLeave={(e) => {
              e.currentTarget.style.color = 'var(--color-accent-blue)'
              e.currentTarget.style.background = 'transparent'
            }}
          >
            See all
          </button>
        )}
      </div>

      {decisions.length === 0 ? (
        <div style={{ borderTop: '1px solid var(--color-border)' }}>
          <EmptyState messageKey="dashboard.noHandoff" />
        </div>
      ) : (
        <div role="list">
          {decisions.map((decision) => (
            <div
              key={decision.id}
              role="listitem"
              className="px-4 py-3"
              style={{ borderTop: '1px solid var(--color-border)' }}
            >
              <div className="flex items-center justify-between gap-2 mb-0.5">
                <span
                  className="text-body truncate"
                  style={{ color: 'var(--color-text-primary)' }}
                  title={decision.title}
                >
                  {decision.title}
                </span>
                {decision.repo_name && (
                  <span
                    className="shrink-0 font-mono text-caption rounded px-1.5 py-0.5"
                    style={{
                      color: 'var(--color-accent-blue)',
                      background: 'var(--color-bg-hover)',
                      border: '1px solid var(--color-border)',
                    }}
                  >
                    {decision.repo_name}
                  </span>
                )}
              </div>
              <p className="text-caption" style={{ color: 'var(--color-text-muted)' }}>
                {formatRelative(decision.created_at)}
              </p>
            </div>
          ))}
        </div>
      )}
    </section>
  )
}
