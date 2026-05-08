import { useRef } from 'react'
import { useTranslation } from 'react-i18next'
import type { PendingProposal, ProposalStatus } from '../../types/api'
import { SourceBadge } from './SourceBadge'
import type { SourceType } from './SourceBadge'

interface PendingProposalCardProps {
  proposal: PendingProposal
  onAccept: (id: string) => void
  onReject: (id: string) => void
  isPending: boolean
  error: boolean
  /** Whether this card is in batch-select mode */
  selected: boolean
  onSelectChange: (id: string, checked: boolean) => void
  /** Optimistic override for status (used in pending tab after action) */
  optimisticStatus?: ProposalStatus | null
  /** Optimistic resolved timestamp */
  optimisticResolvedAt?: string | null
}

function toSourceType(raw: string | undefined): SourceType {
  const allowed: SourceType[] = ['article', 'til', 'bookmark', 'zettelkasten', 'agent-proposed']
  if (raw && (allowed as string[]).includes(raw)) return raw as SourceType
  return 'agent-proposed'
}

function formatRelativeTime(dateStr: string): string {
  const diff = Date.now() - new Date(dateStr).getTime()
  const minutes = Math.floor(diff / 60_000)
  if (minutes < 1) return 'just now'
  if (minutes < 60) return `${minutes} min ago`
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return `${hours}h ago`
  return `${Math.floor(hours / 24)}d ago`
}

export function PendingProposalCard({
  proposal,
  onAccept,
  onReject,
  isPending,
  error,
  selected,
  onSelectChange,
  optimisticStatus = null,
  optimisticResolvedAt = null,
}: PendingProposalCardProps) {
  const { t } = useTranslation()
  const dialogRef = useRef<HTMLDialogElement>(null)
  const titleId = `proposal-title-${proposal.id}`
  const checkboxId = `proposal-check-${proposal.id}`

  const sourceType = proposal.payload.source_item_id
    ? toSourceType(proposal.payload.source_item_type)
    : 'agent-proposed'

  // Effective status: use optimistic override if present, else proposal.status
  const effectiveStatus: ProposalStatus = optimisticStatus ?? proposal.status

  // Visual state based on effective status
  const isResolved = effectiveStatus === 'accepted' || effectiveStatus === 'rejected'
  const resolvedAt = optimisticResolvedAt ?? proposal.resolved_at

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

  // Border color based on status
  let borderColor = selected ? 'var(--color-border-focus)' : 'var(--color-border)'
  if (effectiveStatus === 'accepted') borderColor = 'var(--color-success)'
  if (effectiveStatus === 'rejected') borderColor = 'var(--color-text-muted)'

  // Left accent border
  let borderLeft = 'none'
  if (effectiveStatus === 'accepted') borderLeft = '3px solid var(--color-success)'
  if (effectiveStatus === 'rejected') borderLeft = '3px solid var(--color-text-muted)'

  return (
    <article
      aria-labelledby={titleId}
      className="rounded-md p-4 mb-3 flex gap-3"
      style={{
        background: selected ? 'var(--color-bg-hover)' : 'var(--color-bg-input)',
        border: `1px solid ${borderColor}`,
        borderLeft: borderLeft !== 'none' ? borderLeft : `1px solid ${borderColor}`,
        opacity: isPending ? 0.7 : isResolved ? 0.85 : 1,
        pointerEvents: isPending ? 'none' : undefined,
        transition: 'opacity 150ms ease, background 150ms ease, border-color 150ms ease',
      }}
    >
      {/* Checkbox column — only show for pending cards in batch mode */}
      {!isResolved && (
        <div className="flex items-start pt-0.5 shrink-0">
          <input
            id={checkboxId}
            type="checkbox"
            checked={selected}
            onChange={(e) => onSelectChange(proposal.id, e.target.checked)}
            aria-label={`Select proposal: ${proposal.payload.title}`}
            style={{
              width: '16px',
              height: '16px',
              cursor: 'pointer',
              accentColor: 'var(--color-accent-blue)',
            }}
          />
        </div>
      )}

      {/* Card body */}
      <div className="flex-1 min-w-0">
        {/* Source badge + status badge row */}
        <div className="mb-2 flex items-center gap-2 flex-wrap">
          <SourceBadge
            type={sourceType}
            sourceItemId={proposal.payload.source_item_id}
          />
          {/* Status badge for resolved cards */}
          {effectiveStatus === 'accepted' && (
            <span
              className="text-label rounded-full px-2 py-0.5 flex items-center gap-1"
              style={{
                background: 'var(--color-status-active-bg)',
                color: 'var(--color-status-active-text)',
              }}
            >
              {t('proposals.statusBadge.accepted', '✓ Accepted')}
            </span>
          )}
          {effectiveStatus === 'rejected' && (
            <span
              className="text-label rounded-full px-2 py-0.5 flex items-center gap-1"
              style={{
                background: 'var(--color-status-archived-bg)',
                color: 'var(--color-status-archived-text)',
              }}
            >
              {t('proposals.statusBadge.rejected', '✗ Rejected')}
            </span>
          )}
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

        {/* Resolved timestamp */}
        {isResolved && resolvedAt && (
          <p
            className="text-caption mb-2"
            style={{ color: 'var(--color-text-muted)' }}
          >
            {effectiveStatus === 'accepted' ? 'Accepted' : 'Rejected'} {formatRelativeTime(resolvedAt)}
          </p>
        )}

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

        {/* Action buttons — only shown for pending cards */}
        {!isResolved && (
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
        )}
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
