import { Zap } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { EmptyState } from '../ui/EmptyState'
import type { SessionHandoff } from '../../types/api'

interface HandoffCardProps {
  handoff: SessionHandoff | null;
}

function formatDate(dateStr: string): string {
  return new Date(dateStr).toLocaleDateString(undefined, {
    month: 'short',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  })
}

export function HandoffCard({ handoff }: HandoffCardProps) {
  const { t } = useTranslation()

  if (!handoff) {
    return <EmptyState messageKey="dashboard.noHandoff" />
  }

  return (
    <article
      aria-label="Session handoff note"
      className="rounded-lg p-4"
      style={{
        background: 'var(--color-bg-card)',
        border: '1px solid var(--color-warning)',
        borderLeft: '4px solid var(--color-warning)',
        opacity: 1,
      }}
    >
      <div className="flex items-center gap-2 mb-3">
        <Zap size={16} aria-hidden="true" style={{ color: 'var(--color-warning)' }} />
        <span className="text-label" style={{ color: 'var(--color-warning)' }}>
          {t('dashboard.sections.nextSession')}
        </span>
      </div>

      <p className="text-body mb-2" style={{ color: 'var(--color-text-primary)' }}>
        {handoff.intent}
      </p>

      {handoff.context_summary && (
        <p
          className="text-body-sm mb-3"
          style={{
            color: 'var(--color-text-muted)',
            display: '-webkit-box',
            WebkitLineClamp: 3,
            WebkitBoxOrient: 'vertical',
            overflow: 'hidden',
          }}
        >
          {handoff.context_summary}
        </p>
      )}

      <div className="flex items-center justify-between">
        {handoff.repo_name && (
          <span className="font-mono text-caption" style={{ color: 'var(--color-accent-blue)' }}>
            {handoff.repo_name}
          </span>
        )}
        <span className="text-caption ml-auto" style={{ color: 'var(--color-text-muted)' }}>
          {formatDate(handoff.created_at)}
        </span>
      </div>
    </article>
  )
}
