import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { ChevronDown, ChevronRight } from 'lucide-react'
import { usePendingProposals } from '../../hooks/usePendingProposals'
import { useResolveProposal } from '../../hooks/useResolveProposal'
import { useToastStore } from '../../stores/toastStore'
import { LoadingSkeleton } from '../ui/LoadingSkeleton'
import { PendingProposalCard } from './PendingProposalCard'

export function PendingProposalsSection() {
  const { t } = useTranslation()
  const { addToast } = useToastStore()
  const [collapsed, setCollapsed] = useState(false)
  const [pendingId, setPendingId] = useState<string | null>(null)
  const [errorId, setErrorId] = useState<string | null>(null)

  const { data: proposals = [], isLoading, isError, refetch } = usePendingProposals()
  const resolveMutation = useResolveProposal()

  // Only render when there's something to show (loading shows skeleton inside,
  // error shows inline banner, empty hides the whole section)
  if (!isLoading && !isError && proposals.length === 0) {
    return null
  }

  function handleAccept(id: string) {
    setPendingId(id)
    setErrorId(null)
    resolveMutation.mutate(
      { id, action: 'accept' },
      {
        onSuccess: () => {
          setPendingId(null)
          addToast({ type: 'success', message: t('knowledge.proposals.acceptedToast') })
        },
        onError: () => {
          setPendingId(null)
          setErrorId(id)
        },
      },
    )
  }

  function handleReject(id: string) {
    setPendingId(id)
    setErrorId(null)
    resolveMutation.mutate(
      { id, action: 'reject' },
      {
        onSuccess: () => {
          setPendingId(null)
          addToast({ type: 'info', message: t('knowledge.proposals.rejectedToast') })
        },
        onError: () => {
          setPendingId(null)
          setErrorId(id)
        },
      },
    )
  }

  const countLabel = isLoading
    ? '…'
    : t('knowledge.proposals.awaiting', { count: proposals.length })

  return (
    <section
      aria-labelledby="proposals-heading"
      className="rounded-lg p-5 mb-6"
      style={{
        background: 'var(--color-bg-card)',
        border: '1px solid var(--color-border)',
      }}
    >
      {/* Section header */}
      <div className="flex items-center justify-between mb-3">
        <div className="flex items-center gap-2">
          <h2
            id="proposals-heading"
            className="text-label"
            style={{ color: 'var(--color-text-muted)' }}
          >
            {t('knowledge.proposals.sectionTitle')}
          </h2>
          {!isLoading && !isError && (
            <span
              className="text-label rounded-full px-2 py-0.5"
              style={{
                background: 'var(--color-status-on-hold-bg)',
                color: 'var(--color-status-on-hold-text)',
              }}
            >
              {countLabel}
            </span>
          )}
        </div>
        <button
          type="button"
          onClick={() => setCollapsed((c) => !c)}
          aria-expanded={!collapsed}
          aria-controls="proposals-list"
          aria-label={collapsed ? t('knowledge.proposals.expand', 'Expand proposals section') : t('knowledge.proposals.collapse', 'Collapse proposals section')}
          className="rounded p-1 transition-colors"
          style={{
            background: 'transparent',
            border: 'none',
            cursor: 'pointer',
            color: 'var(--color-text-muted)',
          }}
          onMouseEnter={(e) => { e.currentTarget.style.background = 'var(--color-bg-hover)' }}
          onMouseLeave={(e) => { e.currentTarget.style.background = 'transparent' }}
        >
          {collapsed
            ? <ChevronRight size={14} aria-hidden="true" />
            : <ChevronDown size={14} aria-hidden="true" />}
        </button>
      </div>

      {/* Body */}
      {!collapsed && (
        <div id="proposals-list">
          {isLoading ? (
            <div className="grid gap-3">
              <LoadingSkeleton className="h-32 w-full" />
              <LoadingSkeleton className="h-32 w-full" />
            </div>
          ) : isError ? (
            <div
              className="rounded-md p-3 text-body-sm flex items-center justify-between"
              style={{
                background: '#2e0a0a',
                border: '1px solid var(--color-error)',
                color: 'var(--color-error)',
              }}
              role="alert"
            >
              <span>{t('error.loadFailed')}</span>
              <button
                type="button"
                onClick={() => void refetch()}
                className="rounded px-2 py-1 text-body-sm transition-opacity hover:opacity-80"
                style={{
                  background: 'var(--color-error)',
                  color: '#fff',
                  border: 'none',
                  cursor: 'pointer',
                }}
              >
                {t('common.retry')}
              </button>
            </div>
          ) : (
            proposals.map((p) => (
              <PendingProposalCard
                key={p.id}
                proposal={p}
                onAccept={handleAccept}
                onReject={handleReject}
                isPending={pendingId === p.id}
                error={errorId === p.id}
              />
            ))
          )}
        </div>
      )}
    </section>
  )
}
