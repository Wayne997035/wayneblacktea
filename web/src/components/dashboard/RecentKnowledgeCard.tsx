import { useTranslation } from 'react-i18next'
import { LoadingSkeleton } from '../ui/LoadingSkeleton'
import { EmptyState } from '../ui/EmptyState'
import { useRecentKnowledge } from '../../hooks/useRecentKnowledge'
import type { KnowledgeType } from '../../types/api'

interface RecentKnowledgeCardProps {
  onClick: () => void
}

interface TypeBadgeStyle {
  abbrev: string
  color: string
}

function getTypeBadgeStyle(type: KnowledgeType): TypeBadgeStyle {
  switch (type) {
    case 'article':
      return { abbrev: 'art', color: 'var(--color-accent-purple)' }
    case 'til':
      return { abbrev: 'til', color: 'var(--color-success)' }
    case 'bookmark':
      return { abbrev: 'bkm', color: 'var(--color-warning)' }
    case 'zettelkasten':
      return { abbrev: 'ztl', color: 'var(--color-accent-blue)' }
  }
}

export function RecentKnowledgeCard({ onClick }: RecentKnowledgeCardProps) {
  const { t } = useTranslation()
  const { data, isLoading } = useRecentKnowledge()

  if (isLoading) {
    return (
      <div
        className="rounded-lg"
        style={{
          background: 'var(--color-bg-card)',
          border: '1px solid var(--color-border)',
        }}
      >
        <div className="px-4 pt-4 pb-3">
          <LoadingSkeleton style={{ width: '60%', height: '14px' }} />
        </div>
        {Array.from({ length: 3 }, (_, i) => (
          <div
            key={i}
            className="px-4 py-3"
            style={{ borderTop: '1px solid var(--color-border)' }}
          >
            <div className="flex items-center justify-between gap-2 mb-1">
              <LoadingSkeleton style={{ width: `${70 + i * 10}%`, height: '14px' }} />
              <LoadingSkeleton style={{ width: '40px', height: '14px' }} />
            </div>
            <div className="flex gap-1.5">
              <LoadingSkeleton style={{ width: '50px', height: '12px' }} />
              <LoadingSkeleton style={{ width: '40px', height: '12px' }} />
            </div>
          </div>
        ))}
      </div>
    )
  }

  const items = data ?? []

  if (items.length === 0) {
    return (
      <div
        className="rounded-lg"
        style={{
          background: 'var(--color-bg-card)',
          border: '1px solid var(--color-border)',
        }}
      >
        <div className="px-4 pt-4 pb-3 text-label" style={{ color: 'var(--color-text-muted)' }}>
          {t('dashboard.widgets.recentKnowledge')}
        </div>
        <div style={{ borderTop: '1px solid var(--color-border)' }}>
          <EmptyState messageKey="knowledge.noItems" />
        </div>
      </div>
    )
  }

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
      aria-label="Recent knowledge items"
      className="rounded-lg transition-colors"
      style={{
        background: 'var(--color-bg-card)',
        border: '1px solid var(--color-border)',
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
      <div className="px-4 pt-4 pb-3 text-label" style={{ color: 'var(--color-text-muted)' }}>
        {t('dashboard.widgets.recentKnowledge')}
      </div>

      {items.map((item) => {
        const badgeStyle = getTypeBadgeStyle(item.type)
        const extraTags = item.tags.length > 3 ? item.tags.length - 2 : 0
        const displayTags = item.tags.slice(0, extraTags > 0 ? 2 : 3)

        return (
          <div
            key={item.id}
            className="px-4 py-3"
            style={{ borderTop: '1px solid var(--color-border)' }}
          >
            <div className="flex items-center justify-between gap-2 mb-1">
              <span
                className="text-body truncate"
                style={{ color: 'var(--color-text-primary)' }}
                title={item.title}
              >
                {item.title}
              </span>
              <span
                aria-hidden="true"
                className="shrink-0 rounded font-mono text-caption px-1 py-0.5"
                style={{
                  color: badgeStyle.color,
                  background: 'var(--color-bg-hover)',
                  border: '1px solid var(--color-border)',
                }}
              >
                {badgeStyle.abbrev}
              </span>
            </div>
            {displayTags.length > 0 && (
              <div className="flex items-center gap-1.5 flex-wrap">
                {displayTags.map((tag) => (
                  <span
                    key={tag}
                    aria-hidden="true"
                    className="rounded text-caption px-1.5 py-0.5"
                    style={{
                      background: 'var(--color-bg-hover)',
                      color: 'var(--color-text-muted)',
                      border: '1px solid var(--color-border)',
                    }}
                  >
                    {tag}
                  </span>
                ))}
                {extraTags > 0 && (
                  <span
                    aria-hidden="true"
                    className="rounded text-caption px-1.5 py-0.5"
                    style={{
                      background: 'var(--color-bg-hover)',
                      color: 'var(--color-text-muted)',
                      border: '1px solid var(--color-border)',
                    }}
                  >
                    +{extraTags}
                  </span>
                )}
              </div>
            )}
          </div>
        )
      })}
    </article>
  )
}
