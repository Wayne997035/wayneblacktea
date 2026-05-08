import { useTranslation } from 'react-i18next'
import { LoadingSkeleton } from '../ui/LoadingSkeleton'
import { EmptyState } from '../ui/EmptyState'
import { useDueReviewsDashboard } from '../../hooks/useDueReviewsDashboard'

interface DueReviewsCardProps {
  onClick: () => void
}

export function DueReviewsCard({ onClick }: DueReviewsCardProps) {
  const { t } = useTranslation()
  const { data, isLoading } = useDueReviewsDashboard()

  if (isLoading) {
    return (
      <div
        className="rounded-lg p-4"
        style={{
          background: 'var(--color-bg-card)',
          border: '1px solid var(--color-border)',
        }}
      >
        <div className="flex items-center justify-between mb-2">
          <LoadingSkeleton style={{ width: '50%', height: '14px' }} />
          <LoadingSkeleton style={{ width: '24px', height: '24px' }} />
        </div>
        <LoadingSkeleton style={{ width: '90%', height: '12px' }} />
        <div className="mt-2">
          <LoadingSkeleton style={{ width: '75%', height: '12px' }} />
        </div>
        <div className="mt-2">
          <LoadingSkeleton style={{ width: '80%', height: '12px' }} />
        </div>
      </div>
    )
  }

  const reviews = data ?? []
  const count = reviews.length

  if (count === 0) {
    return (
      <div
        className="rounded-lg p-4"
        style={{
          background: 'var(--color-bg-card)',
          border: '1px solid var(--color-border)',
        }}
      >
        <div className="text-label mb-2" style={{ color: 'var(--color-text-muted)' }}>
          {t('dashboard.widgets.dueReviews')}
        </div>
        <EmptyState messageKey="reviews.noDue" />
      </div>
    )
  }

  const visibleReviews = reviews.slice(0, 3)
  const remaining = count - 3

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === 'Enter' || e.key === ' ') {
      e.preventDefault()
      onClick()
    }
  }

  return (
    <article
      role="button"
      tabIndex={0}
      aria-label={`Due reviews: ${count} concepts due`}
      className="rounded-lg p-4 transition-colors"
      style={{
        background: 'var(--color-bg-card)',
        border: '1px solid var(--color-border)',
        borderLeft: '4px solid var(--color-accent-blue)',
        cursor: 'pointer',
      }}
      onClick={onClick}
      onKeyDown={handleKeyDown}
      onMouseEnter={(e) => {
        e.currentTarget.style.background = 'var(--color-bg-hover)'
      }}
      onMouseLeave={(e) => {
        e.currentTarget.style.background = 'var(--color-bg-card)'
      }}
    >
      <div className="flex items-center justify-between mb-3">
        <span className="text-label" style={{ color: 'var(--color-text-muted)' }}>
          {t('dashboard.widgets.dueReviews')}
        </span>
        <span
          className="rounded-full font-mono text-caption px-2 py-0.5"
          style={{
            background: '#0a1f35',
            color: 'var(--color-accent-blue)',
            border: '1px solid var(--color-accent-blue)',
          }}
        >
          {t('dashboard.widgets.dueReviewsCount', { count })}
        </span>
      </div>

      <div role="list" className="flex flex-col gap-1">
        {visibleReviews.map((review) => (
          <div key={review.schedule_id} role="listitem" className="flex items-center gap-2">
            <span
              aria-hidden="true"
              className="w-1.5 h-1.5 rounded-full shrink-0"
              style={{ background: 'var(--color-accent-blue)' }}
            />
            <span
              className="text-body-sm truncate"
              style={{ color: 'var(--color-text-primary)' }}
              title={review.title}
            >
              {review.title}
            </span>
          </div>
        ))}
      </div>

      {remaining > 0 && (
        <p className="text-caption mt-1" style={{ color: 'var(--color-text-muted)' }}>
          {t('dashboard.widgets.moreItems', { count: remaining })}
        </p>
      )}
    </article>
  )
}
