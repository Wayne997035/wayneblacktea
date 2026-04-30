import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import type { Decision } from '../../types/api'

interface DecisionEntryProps {
  decision: Decision;
}

function formatDate(dateStr: string): string {
  return new Date(dateStr).toLocaleString(undefined, {
    year: 'numeric',
    month: 'short',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
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
        <div className="flex-1 min-w-0">
          <h3 className="text-card-title" style={{ color: 'var(--color-text-primary)' }}>
            {decision.title}
          </h3>
          {decision.repo_name && (
            <span
              className="text-label rounded px-1.5 py-0.5 mt-0.5 inline-block"
              style={{
                background: 'var(--color-bg-hover)',
                color: 'var(--color-accent-blue)',
                border: '1px solid var(--color-border)',
              }}
            >
              {decision.repo_name}
            </span>
          )}
        </div>
        <time
          dateTime={decision.created_at}
          className="text-caption shrink-0"
          style={{ color: 'var(--color-text-muted)' }}
        >
          {formatDate(decision.created_at)}
        </time>
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
