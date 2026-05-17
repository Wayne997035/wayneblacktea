import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { CheckCircle, XCircle } from 'lucide-react'
import { useAllProposals } from '../hooks/usePendingProposals'
import { useResolveProposal } from '../hooks/useResolveProposal'
import { LoadingSkeleton } from '../components/ui/LoadingSkeleton'
import { EmptyState } from '../components/ui/EmptyState'
import type { PendingProposal } from '../types/api'

type Tab = 'pending' | 'accepted' | 'rejected' | 'all'

function ProposalTypeBadge({ type }: { type: PendingProposal['type'] }) {
  const colorMap: Record<PendingProposal['type'], { bg: string; text: string }> = {
    goal:      { bg: 'var(--color-status-active-bg)',    text: 'var(--color-status-active-text)' },
    project:   { bg: 'var(--color-status-on-hold-bg)',   text: 'var(--color-status-on-hold-text)' },
    task:      { bg: 'var(--color-status-archived-bg)',  text: 'var(--color-status-archived-text)' },
    concept:   { bg: 'var(--color-accent-blue-bg, rgba(88,166,255,.15))', text: 'var(--color-accent-blue)' },
    knowledge: { bg: 'var(--color-status-completed-bg)', text: 'var(--color-status-completed-text)' },
    decision:  { bg: 'var(--color-status-on-hold-bg, rgba(210,153,34,.15))', text: 'var(--color-status-on-hold-text)' },
  }
  const { bg, text } = colorMap[type]
  return (
    <span
      className="px-2 py-0.5 rounded-full font-mono text-label whitespace-nowrap uppercase"
      style={{ background: bg, color: text, fontSize: '10px' }}
    >
      {type}
    </span>
  )
}

function ProposalStatusBadge({ status }: { status: PendingProposal['status'] }) {
  const { t } = useTranslation()
  const styleMap: Record<PendingProposal['status'], { bg: string; text: string }> = {
    pending:  { bg: 'var(--color-status-on-hold-bg)',   text: 'var(--color-status-on-hold-text)' },
    accepted: { bg: 'var(--color-status-completed-bg)', text: 'var(--color-status-completed-text)' },
    rejected: { bg: 'var(--color-status-archived-bg)',  text: 'var(--color-status-archived-text)' },
  }
  const { bg, text } = styleMap[status]
  return (
    <span
      className="px-2 py-0.5 rounded-full font-mono text-label whitespace-nowrap"
      style={{ background: bg, color: text, fontSize: '10px' }}
    >
      {t(`proposals.statusBadge.${status}`)}
    </span>
  )
}

function ProposalCard({ proposal }: { proposal: PendingProposal }) {
  const { t } = useTranslation()
  const { mutate: resolve, isPending, isError } = useResolveProposal()
  const [confirming, setConfirming] = useState(false)

  const title = 'title' in proposal.payload ? proposal.payload.title : ''
  const content = 'content' in proposal.payload ? proposal.payload.content : ''

  function handleAccept() {
    resolve({ id: proposal.id, action: 'accept' })
  }

  function handleReject() {
    if (!confirming) {
      setConfirming(true)
      return
    }
    resolve({ id: proposal.id, action: 'reject' })
    setConfirming(false)
  }

  return (
    <article
      className="rounded-lg p-4"
      style={{
        background: 'var(--color-bg-card)',
        border: '1px solid var(--color-border)',
      }}
    >
      <div className="flex items-start justify-between gap-3 mb-2">
        <div className="flex items-center gap-2 flex-wrap">
          <ProposalTypeBadge type={proposal.type} />
          {proposal.status !== 'pending' && (
            <ProposalStatusBadge status={proposal.status} />
          )}
        </div>
        <span className="text-caption shrink-0" style={{ color: 'var(--color-text-muted)' }}>
          {new Date(proposal.created_at).toLocaleDateString(undefined, {
            month: 'short',
            day: 'numeric',
          })}
        </span>
      </div>

      {title && (
        <p className="text-body font-medium mb-1" style={{ color: 'var(--color-text-primary)' }}>
          {title}
        </p>
      )}

      {content && (
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
          {content}
        </p>
      )}

      {isError && (
        <p
          className="text-body-sm mt-2"
          role="alert"
          style={{ color: 'var(--color-error)' }}
        >
          {t('common.errors.saveFailed')}
        </p>
      )}

      {proposal.status === 'pending' && (
        <div className="flex items-center gap-2 mt-3">
          <button
            type="button"
            disabled={isPending}
            onClick={handleAccept}
            className="flex items-center gap-1.5 px-3 py-1.5 rounded-md text-body-sm transition-colors"
            style={{
              background: 'var(--color-status-completed-bg)',
              color: 'var(--color-status-completed-text)',
              border: 'none',
              cursor: isPending ? 'not-allowed' : 'pointer',
              opacity: isPending ? 0.6 : 1,
            }}
          >
            <CheckCircle size={13} aria-hidden="true" />
            {t('knowledge.proposals.accept')}
          </button>

          <button
            type="button"
            disabled={isPending}
            onClick={handleReject}
            className="flex items-center gap-1.5 px-3 py-1.5 rounded-md text-body-sm transition-colors"
            style={{
              background: confirming ? 'var(--color-error-bg, rgba(248,81,73,.15))' : 'var(--color-bg-input)',
              color: confirming ? 'var(--color-error)' : 'var(--color-text-muted)',
              border: `1px solid ${confirming ? 'var(--color-error)' : 'var(--color-border)'}`,
              cursor: isPending ? 'not-allowed' : 'pointer',
              opacity: isPending ? 0.6 : 1,
            }}
          >
            <XCircle size={13} aria-hidden="true" />
            {confirming ? t('knowledge.proposals.confirmReject') : t('knowledge.proposals.reject')}
          </button>
        </div>
      )}
    </article>
  )
}

const TABS: Tab[] = ['pending', 'accepted', 'rejected', 'all']

export function ProposalsPage() {
  const { t } = useTranslation()
  const [tab, setTab] = useState<Tab>('pending')
  const { data: proposals, isLoading, isError } = useAllProposals(tab)

  return (
    <div className="p-6 max-w-[900px] mx-auto">
      <div className="flex items-center justify-between mb-4">
        <h1 className="text-page-title" style={{ color: 'var(--color-text-primary)' }}>
          {t('knowledge.proposals.sectionTitle')}
        </h1>
      </div>

      {/* Tabs */}
      <div
        role="tablist"
        aria-label={t('knowledge.proposals.sectionTitle')}
        className="flex gap-1 mb-6 p-1 rounded-lg"
        style={{ background: 'var(--color-bg-card)', border: '1px solid var(--color-border)', width: 'fit-content' }}
      >
        {TABS.map((t_) => (
          <button
            key={t_}
            type="button"
            role="tab"
            aria-selected={tab === t_}
            onClick={() => setTab(t_)}
            className="px-4 py-1.5 rounded-md text-body-sm transition-colors"
            style={{
              background: tab === t_ ? 'var(--color-accent-blue)' : 'transparent',
              color: tab === t_ ? 'var(--color-bg-base)' : 'var(--color-text-muted)',
              border: 'none',
              cursor: 'pointer',
            }}
          >
            {t(`proposals.tabs.${t_}`)}
          </button>
        ))}
      </div>

      {isError && (
        <div
          className="rounded-md p-3 mb-6 text-body-sm"
          style={{
            background: '#2e0a0a',
            border: '1px solid var(--color-error)',
            color: 'var(--color-error)',
          }}
          role="alert"
        >
          {t('error.loadFailed')}
        </div>
      )}

      <div role="tabpanel" aria-label={t(`proposals.tabs.${tab}`)}>
        {isLoading ? (
          <div className="space-y-3">
            {Array.from({ length: 4 }, (_, i) => (
              <LoadingSkeleton key={i} className="h-24 w-full" />
            ))}
          </div>
        ) : !proposals || proposals.length === 0 ? (
          <EmptyState messageKey="proposals.allCaughtUp" />
        ) : (
          <div className="space-y-3">
            {proposals.map((p) => (
              <ProposalCard key={p.id} proposal={p} />
            ))}
          </div>
        )}
      </div>
    </div>
  )
}
