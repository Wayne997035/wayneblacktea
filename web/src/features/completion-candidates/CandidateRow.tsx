import { useTranslation } from 'react-i18next'
import type { CompletionCandidate } from './types'
import { safeArtifactHref } from './safeArtifactHref'

interface CandidateRowProps {
  candidate: CompletionCandidate
  /**
   * Optional callback fired when the user clicks "Accept". When omitted (and
   * `status !== 'auto_applied'`) the buttons render in a disabled state with
   * an explanatory "Not yet available" tooltip — they MUST NOT appear active
   * because the backend accept/reject endpoint is not yet wired (PR #126
   * round-1 review feedback).
   */
  onAccept?: (candidate: CompletionCandidate) => void
  /**
   * Optional callback fired when the user clicks "Reject". See `onAccept`.
   */
  onReject?: (candidate: CompletionCandidate) => void
}

export function CandidateRow({ candidate, onAccept, onReject }: CandidateRowProps) {
  const { t } = useTranslation()
  const isAutoApplied = candidate.status === 'auto_applied'
  const safeHref = safeArtifactHref(candidate.suggested_artifact)
  const artifactText = candidate.suggested_artifact ?? ''
  const acceptDisabled = !onAccept
  const rejectDisabled = !onReject
  const notYetAvailable = t('candidates.actions.notYetAvailable')

  return (
    <article
      data-testid="candidate-row"
      data-candidate-id={candidate.id}
      data-reason={candidate.reason}
      data-confidence={candidate.confidence}
      data-status={candidate.status}
      className="flex flex-col gap-2 rounded-md border p-3 bg-[var(--color-bg-card)] border-[var(--color-border)]"
    >
      <header className="flex items-center gap-2">
        <span
          data-testid="reason-label"
          className="text-sm font-medium text-[var(--color-text-primary)]"
        >
          {t(`candidates.reason.${candidate.reason}`)}
        </span>
        <span
          data-testid="confidence-badge"
          data-level={candidate.confidence}
          className="rounded-full px-2 py-0.5 font-mono text-xs uppercase bg-[var(--color-bg-hover)] text-[var(--color-text-muted)]"
        >
          {t(`candidates.confidence.${candidate.confidence}`)}
        </span>
      </header>
      <div data-testid="artifact" className="text-sm">
        <a
          href={safeHref}
          target="_blank"
          rel="noopener noreferrer"
          className="underline text-[var(--color-accent-blue)] hover:text-[var(--color-border-focus)]"
        >
          {artifactText}
        </a>
      </div>
      <div
        data-testid="meta"
        className="flex items-center gap-2 text-xs text-[var(--color-text-muted)]"
      >
        <time dateTime={candidate.created_at}>{candidate.created_at}</time>
        <span data-testid="status">
          {t(`candidates.status.${candidate.status}`)}
        </span>
      </div>
      {!isAutoApplied && (
        <div data-testid="actions" className="flex gap-2">
          <button
            type="button"
            aria-label={t('candidates.actions.acceptAria')}
            aria-disabled={acceptDisabled || undefined}
            disabled={acceptDisabled}
            title={acceptDisabled ? notYetAvailable : undefined}
            onClick={() => onAccept?.(candidate)}
            className="rounded px-2 py-1 text-xs font-medium bg-[var(--color-success)] text-white hover:opacity-90 disabled:cursor-not-allowed disabled:opacity-50"
          >
            {t('candidates.actions.accept')}
          </button>
          <button
            type="button"
            aria-label={t('candidates.actions.rejectAria')}
            aria-disabled={rejectDisabled || undefined}
            disabled={rejectDisabled}
            title={rejectDisabled ? notYetAvailable : undefined}
            onClick={() => onReject?.(candidate)}
            className="rounded px-2 py-1 text-xs font-medium bg-[var(--color-bg-hover)] text-[var(--color-text-primary)] hover:opacity-90 disabled:cursor-not-allowed disabled:opacity-50"
          >
            {t('candidates.actions.reject')}
          </button>
        </div>
      )}
    </article>
  )
}
