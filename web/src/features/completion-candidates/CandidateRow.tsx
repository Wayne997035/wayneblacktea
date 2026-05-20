import { useTranslation } from 'react-i18next'
import type { CompletionCandidate } from './types'
import { safeArtifactHref } from './safeArtifactHref'

interface CandidateRowProps {
  candidate: CompletionCandidate
  /**
   * Optional callback fired when the user clicks "Accept". When omitted (and
   * `status !== 'auto_applied'`) the buttons still render — clicks are no-ops.
   * This keeps the design contract stable: the buttons are always present for
   * pending/accepted/rejected rows so a snapshot test can assert their
   * presence without mocking handlers.
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

  return (
    <article
      data-testid="candidate-row"
      data-candidate-id={candidate.id}
      data-reason={candidate.reason}
      data-confidence={candidate.confidence}
      data-status={candidate.status}
      className="flex flex-col gap-2 rounded-md border border-slate-200 bg-white p-3"
    >
      <header className="flex items-center gap-2">
        <span
          data-testid="reason-label"
          className="text-sm font-medium text-slate-700"
        >
          {t(`candidates.reason.${candidate.reason}`)}
        </span>
        <span
          data-testid="confidence-badge"
          data-level={candidate.confidence}
          className="rounded-full bg-slate-100 px-2 py-0.5 font-mono text-xs uppercase text-slate-600"
        >
          {candidate.confidence}
        </span>
      </header>
      <div data-testid="artifact" className="text-sm">
        <a
          href={safeHref}
          target="_blank"
          rel="noopener noreferrer"
          className="text-blue-600 underline hover:text-blue-700"
        >
          {artifactText}
        </a>
      </div>
      <div
        data-testid="meta"
        className="flex items-center gap-2 text-xs text-slate-500"
      >
        <time dateTime={candidate.created_at}>{candidate.created_at}</time>
        <span data-testid="status">{candidate.status}</span>
      </div>
      {!isAutoApplied && (
        <div data-testid="actions" className="flex gap-2">
          <button
            type="button"
            aria-label={t('candidates.actions.acceptAria')}
            onClick={() => onAccept?.(candidate)}
            className="rounded bg-emerald-600 px-2 py-1 text-xs font-medium text-white hover:bg-emerald-700"
          >
            {t('candidates.actions.accept')}
          </button>
          <button
            type="button"
            aria-label={t('candidates.actions.rejectAria')}
            onClick={() => onReject?.(candidate)}
            className="rounded bg-slate-200 px-2 py-1 text-xs font-medium text-slate-700 hover:bg-slate-300"
          >
            {t('candidates.actions.reject')}
          </button>
        </div>
      )}
    </article>
  )
}
