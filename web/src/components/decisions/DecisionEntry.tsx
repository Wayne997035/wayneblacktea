import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import type { Decision } from '../../types/api'

interface DecisionEntryProps {
  decision: Decision;
}

function formatDate(dateStr: string): string {
  return new Date(dateStr).toLocaleDateString(undefined, {
    year: 'numeric',
    month: 'short',
    day: 'numeric',
  })
}

export function DecisionEntry({ decision }: DecisionEntryProps) {
  const { t } = useTranslation()
  const [isExpanded, setIsExpanded] = useState(false)
  const bodyId = `decision-body-${decision.id}`

  return (
    <article
      aria-label={decision.title}
      className="relative pl-6"
    >
      {/* Timeline dot */}
      <span
        className="absolute left-0 top-1.5 w-2.5 h-2.5 rounded-full -translate-x-[5px]"
        style={{
          background: 'var(--color-accent-blue)',
          border: '2px solid var(--color-bg-base)',
        }}
        aria-hidden="true"
      />

      <div className="flex items-start justify-between gap-4 mb-1">
        <h3 className="text-card-title" style={{ color: 'var(--color-text-primary)' }}>
          {decision.title}
        </h3>
        <span className="text-caption shrink-0" style={{ color: 'var(--color-text-muted)' }}>
          {formatDate(decision.created_at)}
        </span>
      </div>

      <p
        className="text-body-sm mb-2"
        style={{
          color: 'var(--color-text-muted)',
          display: '-webkit-box',
          WebkitLineClamp: isExpanded ? 'unset' : 2,
          WebkitBoxOrient: 'vertical',
          overflow: isExpanded ? 'visible' : 'hidden',
        }}
      >
        {decision.rationale}
      </p>

      <button
        type="button"
        onClick={() => setIsExpanded((v) => !v)}
        aria-expanded={isExpanded}
        aria-controls={bodyId}
        className="text-caption underline-offset-2 hover:underline transition-colors"
        style={{ color: 'var(--color-accent-blue)', background: 'transparent', border: 'none', cursor: 'pointer', padding: 0 }}
      >
        {isExpanded ? t('decisions.collapse') : t('decisions.showFull')}
      </button>

      {isExpanded && (
        <div
          id={bodyId}
          className="mt-3 space-y-3 pt-3"
          style={{ borderTop: '1px solid var(--color-border)' }}
        >
          <div>
            <div className="text-label mb-1" style={{ color: 'var(--color-text-muted)' }}>
              {t('decisions.context')}
            </div>
            <p className="text-body-sm" style={{ color: 'var(--color-text-primary)' }}>
              {decision.context}
            </p>
          </div>
          <div>
            <div className="text-label mb-1" style={{ color: 'var(--color-text-muted)' }}>
              {t('decisions.decision')}
            </div>
            <p className="text-body-sm" style={{ color: 'var(--color-text-primary)' }}>
              {decision.decision}
            </p>
          </div>
          {decision.alternatives && (
            <div>
              <div className="text-label mb-1" style={{ color: 'var(--color-text-muted)' }}>
                {t('decisions.alternatives')}
              </div>
              <p className="text-body-sm" style={{ color: 'var(--color-text-primary)' }}>
                {decision.alternatives}
              </p>
            </div>
          )}
        </div>
      )}
    </article>
  )
}
