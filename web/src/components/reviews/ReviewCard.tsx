import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import type { DueReview } from '../../types/api'
import { useSubmitReview } from '../../hooks/useReviews'

interface ReviewCardProps {
  review: DueReview
}

interface RatingButton {
  rating: 1 | 2 | 3 | 4
  labelKey: string
  color: string
}

const RATING_BUTTONS: RatingButton[] = [
  { rating: 1, labelKey: 'reviews.ratings.again', color: '#ef4444' },
  { rating: 2, labelKey: 'reviews.ratings.hard',  color: '#f97316' },
  { rating: 3, labelKey: 'reviews.ratings.good',  color: '#22c55e' },
  { rating: 4, labelKey: 'reviews.ratings.easy',  color: '#3b82f6' },
]

function formatDueDate(iso: string): string {
  const date = new Date(iso)
  const now = new Date()
  const diffMs = now.getTime() - date.getTime()
  const diffDays = Math.floor(diffMs / (1000 * 60 * 60 * 24))

  if (diffDays < 0) return date.toLocaleDateString()
  if (diffDays === 0) return 'Today'
  if (diffDays === 1) return 'Yesterday'
  return `${diffDays}d ago`
}

function estimateNextDays(rating: 1 | 2 | 3 | 4, reviewCount: number): number {
  switch (rating) {
    case 1: return 1
    case 2: return 3
    case 3: return Math.max(7, reviewCount * 3)
    case 4: return Math.max(14, reviewCount * 5)
  }
}

export function ReviewCard({ review }: ReviewCardProps) {
  const { t } = useTranslation()
  const { mutate: submitReview, isPending, variables } = useSubmitReview()
  const [rated, setRated] = useState<{ nextDays: number } | null>(null)

  const isPendingForThis = isPending && variables?.scheduleId === review.schedule_id

  function handleRating(rating: 1 | 2 | 3 | 4) {
    submitReview({
      scheduleId: review.schedule_id,
      rating,
      stability: review.stability,
      difficulty: review.difficulty,
      review_count: review.review_count,
    })
    setRated({ nextDays: estimateNextDays(rating, review.review_count) })
  }

  if (rated) {
    return (
      <div
        style={{
          background: 'var(--color-bg-card)',
          border: '1px solid #22c55e',
          borderRadius: '8px',
          padding: '16px',
          display: 'flex',
          alignItems: 'center',
          gap: '8px',
        }}
        role="status"
        aria-live="polite"
      >
        <span style={{ color: '#22c55e', fontWeight: 600 }}>✓ 已記錄</span>
        <span style={{ color: 'var(--color-text-muted)' }}>
          · 下次複習：{rated.nextDays}天後
        </span>
      </div>
    )
  }

  return (
    <article
      className="rounded-lg p-4 flex flex-col gap-3"
      style={{
        background: 'var(--color-bg-card)',
        border: '1px solid var(--color-border)',
      }}
    >
      {/* Header row */}
      <div className="flex items-start gap-2">
        <h3
          className="text-card-title flex-1 min-w-0 truncate"
          style={{ color: 'var(--color-text-primary)' }}
          title={review.title}
        >
          {review.title}
        </h3>
        <div className="flex items-center gap-2 shrink-0">
          <span
            className="text-caption whitespace-nowrap"
            style={{ color: 'var(--color-text-muted)' }}
          >
            {formatDueDate(review.due_date)}
          </span>
          <span
            className="text-label rounded-full px-2 py-0.5 whitespace-nowrap"
            style={{
              background: 'var(--color-bg-hover)',
              color: 'var(--color-text-muted)',
              border: '1px solid var(--color-border)',
            }}
          >
            {t('reviews.reviewCount', { count: review.review_count })}
          </span>
        </div>
      </div>

      {/* Content */}
      <p
        className="text-body overflow-hidden"
        style={{
          color: 'var(--color-text-muted)',
          display: '-webkit-box',
          WebkitLineClamp: 3,
          WebkitBoxOrient: 'vertical',
          wordBreak: 'break-word',
        }}
      >
        {review.content}
      </p>

      {/* Rating buttons */}
      <div className="grid grid-cols-4 gap-2">
        {RATING_BUTTONS.map(({ rating, labelKey, color }) => (
          <button
            key={rating}
            type="button"
            onClick={() => handleRating(rating)}
            disabled={isPendingForThis}
            aria-label={t(labelKey)}
            className="flex items-center justify-center rounded-lg font-semibold transition-opacity active:scale-95"
            style={{
              minHeight: '44px',
              fontSize: 'var(--text-body-sm, 0.8125rem)',
              fontWeight: 600,
              color: '#ffffff',
              background: color,
              border: 'none',
              borderRadius: '8px',
              flex: 1,
              cursor: isPendingForThis ? 'not-allowed' : 'pointer',
              opacity: isPendingForThis ? 0.45 : 1,
              transition: 'opacity 150ms ease, transform 100ms ease',
            }}
          >
            {t(labelKey)}
          </button>
        ))}
      </div>
    </article>
  )
}
