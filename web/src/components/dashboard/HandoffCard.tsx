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

      {handoff.next_actions && handoff.next_actions.length > 0 && (
        <ol className="mt-3 mb-3 space-y-2" aria-label="Next actions">
          {handoff.next_actions.map((action) => (
            <li key={action.step} className="flex items-start gap-2">
              {/* Step number badge */}
              <span
                className="shrink-0 flex items-center justify-center rounded-full font-mono font-bold"
                style={{
                  width: '18px',
                  height: '18px',
                  fontSize: '10px',
                  background: 'var(--color-bg-input)',
                  color: 'var(--color-text-muted)',
                  border: '1px solid var(--color-border)',
                  marginTop: '1px',
                }}
              >
                {action.step}
              </span>

              <div className="flex-1 min-w-0">
                <div className="flex items-center gap-2 flex-wrap">
                  <span className="text-body-sm" style={{ color: 'var(--color-text-primary)' }}>
                    {action.title}
                  </span>
                  {/* Status badge */}
                  <span
                    className="px-1.5 py-0 rounded font-mono whitespace-nowrap"
                    style={{
                      fontSize: '10px',
                      background:
                        action.status === 'done'
                          ? 'var(--color-status-completed-bg)'
                          : action.status === 'skipped'
                            ? 'var(--color-status-archived-bg)'
                            : 'var(--color-status-on-hold-bg)',
                      color:
                        action.status === 'done'
                          ? 'var(--color-status-completed-text)'
                          : action.status === 'skipped'
                            ? 'var(--color-status-archived-text)'
                            : 'var(--color-status-on-hold-text)',
                    }}
                  >
                    {action.status}
                  </span>
                </div>
                {action.command && (
                  <code
                    className="block mt-0.5 text-caption font-mono truncate"
                    style={{ color: 'var(--color-text-muted)' }}
                    title={action.command}
                  >
                    {action.command}
                  </code>
                )}
              </div>
            </li>
          ))}
        </ol>
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
