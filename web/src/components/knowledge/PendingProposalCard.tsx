import { useRef } from 'react'
import { useTranslation } from 'react-i18next'
import type { PendingProposal } from '../../types/api'

interface PendingProposalCardProps {
  proposal: PendingProposal
  onAccept: (id: string) => void
  onReject: (id: string) => void
  isPending: boolean
  error: boolean
}

const SOURCE_TYPE_LABEL: Record<string, string> = {
  article: 'Article',
  til: 'TIL',
  bookmark: 'Bookmark',
  zettelkasten: 'Note',
}

export function PendingProposalCard({
  proposal,
  onAccept,
  onReject,
  isPending,
  error,
}: PendingProposalCardProps) {
  const { t } = useTranslation()
  const dialogRef = useRef<HTMLDialogElement>(null)
  const titleId = `proposal-title-${proposal.id}`

  const sourceTypeLabel = proposal.payload.source_item_type
    ? SOURCE_TYPE_LABEL[proposal.payload.source_item_type] ?? proposal.payload.source_item_type
    : null
  const sourceBadge = sourceTypeLabel ? `${sourceTypeLabel}→Concept` : 'Concept'

  function openRejectDialog() {
    dialogRef.current?.showModal()
  }

  function handleDialogCancel() {
    dialogRef.current?.close()
  }

  function handleDialogConfirm() {
    dialogRef.current?.close()
    onReject(proposal.id)
  }

  return (
    <article
      aria-labelledby={titleId}
      className="rounded-md p-4 mb-3"
      style={{
        background: 'var(--color-bg-input)',
        border: '1px solid var(--color-border)',
        opacity: isPending ? 0.7 : 1,
        pointerEvents: isPending ? 'none' : undefined,
        transition: 'opacity 150ms ease',
      }}
    >
      {/* Source-type badge */}
      <div className="mb-2">
        <span
          className="text-label rounded px-2 py-0.5"
          style={{
            background: 'var(--color-bg-hover)',
            color: 'var(--color-accent-blue)',
            border: '1px solid var(--color-border)',
          }}
        >
          {sourceBadge}
        </span>
      </div>

      {/* Title */}
      <h3
        id={titleId}
        className="text-card-title mb-1"
        style={{ color: 'var(--color-text-primary)' }}
      >
        {proposal.payload.title}
      </h3>

      {/* Content (3-line clamp) */}
      {proposal.payload.content && (
        <p
          className="text-body-sm mb-2"
          style={{
            color: 'var(--color-text-muted)',
            display: '-webkit-box',
            WebkitLineClamp: 3,
            WebkitBoxOrient: 'vertical',
            overflow: 'hidden',
          }}
        >
          {proposal.payload.content}
        </p>
      )}

      {/* Tags */}
      {proposal.payload.tags && proposal.payload.tags.length > 0 && (
        <div className="flex flex-wrap gap-1 mb-2">
          {proposal.payload.tags.map((tag) => (
            <span
              key={tag}
              className="text-label rounded-full px-2 py-0.5"
              style={{
                background: 'var(--color-bg-hover)',
                color: 'var(--color-text-muted)',
              }}
            >
              #{tag}
            </span>
          ))}
        </div>
      )}

      {/* Source attribution */}
      <p
        className="text-caption mb-3"
        style={{ color: 'var(--color-text-muted)' }}
      >
        {proposal.payload.source_item_id
          ? `${t('common.from', 'from')}: ${sourceTypeLabel ?? 'knowledge'}`
          : 'from: agent-proposed'}
      </p>

      {/* Mutation error */}
      {error && (
        <p
          className="text-body-sm mb-2"
          style={{ color: 'var(--color-error)' }}
          role="alert"
        >
          {t('error.loadFailed')} — {t('common.retry')}
        </p>
      )}

      {/* Action buttons */}
      <div className="flex justify-end gap-2">
        <button
          type="button"
          onClick={openRejectDialog}
          disabled={isPending}
          aria-busy={isPending}
          className="rounded-md px-4 py-2 text-body-sm transition-colors"
          style={{
            background: 'transparent',
            border: '1px solid var(--color-border)',
            color: 'var(--color-text-muted)',
            cursor: isPending ? 'not-allowed' : 'pointer',
          }}
        >
          {t('knowledge.proposals.reject')}
        </button>
        <button
          type="button"
          onClick={() => onAccept(proposal.id)}
          disabled={isPending}
          aria-busy={isPending}
          className="rounded-md px-4 py-2 text-body-sm transition-opacity"
          style={{
            background: 'var(--color-accent-blue)',
            color: 'var(--color-bg-base)',
            border: 'none',
            cursor: isPending ? 'not-allowed' : 'pointer',
            opacity: isPending ? 0.6 : 1,
          }}
        >
          {t('knowledge.proposals.accept')}
        </button>
      </div>

      {/* Reject confirmation dialog */}
      <dialog
        ref={dialogRef}
        aria-labelledby={`reject-dialog-title-${proposal.id}`}
        aria-describedby={`reject-dialog-desc-${proposal.id}`}
        style={{ width: '100%', maxWidth: '360px' }}
      >
        <div
          className="rounded-lg p-5"
          style={{
            background: 'var(--color-bg-card)',
            border: '1px solid var(--color-border)',
          }}
        >
          <h3
            id={`reject-dialog-title-${proposal.id}`}
            className="text-card-title mb-2"
            style={{ color: 'var(--color-text-primary)' }}
          >
            {t('knowledge.proposals.confirmReject')}
          </h3>
          <p
            id={`reject-dialog-desc-${proposal.id}`}
            className="text-body-sm mb-4"
            style={{ color: 'var(--color-text-muted)' }}
          >
            {proposal.payload.title}
          </p>
          <div className="flex justify-end gap-2">
            <button
              type="button"
              onClick={handleDialogCancel}
              className="rounded-md px-4 py-2 text-body-sm"
              style={{
                background: 'transparent',
                border: '1px solid var(--color-border)',
                color: 'var(--color-text-muted)',
                cursor: 'pointer',
              }}
            >
              {t('common.cancel')}
            </button>
            <button
              type="button"
              onClick={handleDialogConfirm}
              className="rounded-md px-4 py-2 text-body-sm"
              style={{
                background: 'var(--color-error)',
                color: '#fff',
                border: 'none',
                cursor: 'pointer',
              }}
            >
              {t('knowledge.proposals.reject')}
            </button>
          </div>
        </div>
      </dialog>
    </article>
  )
}
