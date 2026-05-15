import { useState, useRef } from 'react'
import { useTranslation } from 'react-i18next'
import { useSearchParams } from 'react-router-dom'
import { ChevronDown, ChevronRight } from 'lucide-react'
import { usePendingProposals, useAllProposals } from '../../hooks/usePendingProposals'
import { useResolveProposal } from '../../hooks/useResolveProposal'
import { useConfirmBatchProposals } from '../../hooks/useConfirmBatchProposals'
import { useToastStore } from '../../stores/toastStore'
import { LoadingSkeleton } from '../ui/LoadingSkeleton'
import { PendingProposalCard } from './PendingProposalCard'
import type { PendingProposal, ProposalType, ProposalStatus } from '../../types/api'

// ----- types -----

interface GroupState {
  collapsed: boolean
}

// Optimistic override: tracks cards that have been acted on in "pending" tab
interface OptimisticResolution {
  status: ProposalStatus
  resolvedAt: string
}

// ----- helpers -----

const TYPE_ORDER: ProposalType[] = ['goal', 'project', 'task', 'concept', 'knowledge']
const TYPE_LABELS: Record<ProposalType, string> = {
  goal: 'Goals',
  project: 'Projects',
  task: 'Tasks',
  concept: 'Concepts',
  knowledge: 'Knowledge',
}

const VALID_STATUSES = ['pending', 'accepted', 'rejected', 'all'] as const
type TabStatus = typeof VALID_STATUSES[number]

function isValidStatus(s: string | null): s is TabStatus {
  return VALID_STATUSES.includes(s as TabStatus)
}

function groupByType(proposals: PendingProposal[]): Record<ProposalType, PendingProposal[]> {
  const result: Record<ProposalType, PendingProposal[]> = {
    goal: [], project: [], task: [], concept: [], knowledge: [],
  }
  for (const p of proposals) {
    if (p.type in result) {
      result[p.type as ProposalType].push(p)
    }
  }
  return result
}

// ----- component -----

export function PendingProposalsSection() {
  const { t } = useTranslation()
  const { addToast } = useToastStore()

  // URL search param for active tab status
  const [searchParams, setSearchParams] = useSearchParams()
  const rawStatus = searchParams.get('status')
  const activeTab: TabStatus = isValidStatus(rawStatus) ? rawStatus : 'pending'

  function setActiveTab(tab: TabStatus) {
    setSearchParams((prev) => {
      const next = new URLSearchParams(prev)
      next.set('status', tab)
      return next
    })
  }

  // Section collapse
  const [sectionCollapsed, setSectionCollapsed] = useState(false)

  // Per-type group collapse state
  const [groupState, setGroupState] = useState<Record<ProposalType, GroupState>>({
    goal: { collapsed: false },
    project: { collapsed: false },
    task: { collapsed: false },
    concept: { collapsed: false },
    knowledge: { collapsed: false },
  })

  // Single-item pending state
  const [pendingId, setPendingId] = useState<string | null>(null)
  const [errorId, setErrorId] = useState<string | null>(null)

  // Optimistic resolutions: id -> { status, resolvedAt } for cards acted on in pending tab
  const [optimisticResolutions, setOptimisticResolutions] = useState<
    Map<string, OptimisticResolution>
  >(new Map())

  // Batch selection
  const [selectedIds, setSelectedIds] = useState<Set<string>>(new Set())

  // Batch confirm dialog (for batch >= 5)
  const batchDialogRef = useRef<HTMLDialogElement>(null)
  const [pendingBatchAction, setPendingBatchAction] = useState<'accept' | 'reject' | null>(null)

  // For pending tab: use the original hook (no regression)
  const pendingQuery = usePendingProposals()

  // For other tabs: use the new hook that supports status filtering
  const allQuery = useAllProposals(activeTab)

  const isLoading = activeTab === 'pending' ? pendingQuery.isLoading : allQuery.isLoading
  const isError = activeTab === 'pending' ? pendingQuery.isError : allQuery.isError
  const refetch = activeTab === 'pending' ? pendingQuery.refetch : allQuery.refetch

  // Raw proposals from the query
  const rawProposals: PendingProposal[] = activeTab === 'pending'
    ? (pendingQuery.data ?? [])
    : (allQuery.data ?? [])

  // In the pending tab, merge optimistic resolutions into the card list
  // Cards stay visible but show their new accepted/rejected state
  const proposals: PendingProposal[] = activeTab === 'pending'
    ? rawProposals.map((p) => {
        const override = optimisticResolutions.get(p.id)
        if (override) {
          return { ...p, status: override.status, resolved_at: override.resolvedAt }
        }
        return p
      })
    : rawProposals

  const resolveMutation = useResolveProposal()
  const batchMutation = useConfirmBatchProposals()

  // Tab counts: for pending tab use raw pending count; for others use query data
  const pendingCount = pendingQuery.data?.length ?? 0
  const acceptedCount = activeTab === 'accepted' ? rawProposals.length : null
  const rejectedCount = activeTab === 'rejected' ? rawProposals.length : null
  const allCount = activeTab === 'all' ? rawProposals.length : null

  // Only render when there's something to show
  if (!isLoading && !isError && proposals.length === 0 && activeTab === 'pending') {
    return null
  }

  const grouped = groupByType(proposals)

  // --- Selection helpers ---

  function toggleSelect(id: string, checked: boolean) {
    setSelectedIds((prev) => {
      const next = new Set(prev)
      if (checked) next.add(id)
      else next.delete(id)
      return next
    })
  }

  function toggleSelectAll() {
    // Only allow selecting cards that are still pending
    const selectablePending = proposals.filter((p) => {
      const override = optimisticResolutions.get(p.id)
      return !override
    })
    if (selectedIds.size === selectablePending.length && selectablePending.length > 0) {
      setSelectedIds(new Set())
    } else {
      setSelectedIds(new Set(selectablePending.map((p) => p.id)))
    }
  }

  function selectAllOfType(type: ProposalType) {
    setSelectedIds((prev) => {
      const next = new Set(prev)
      for (const p of grouped[type]) {
        if (!optimisticResolutions.has(p.id)) next.add(p.id)
      }
      return next
    })
  }

  function selectAllAutoSource() {
    setSelectedIds((prev) => {
      const next = new Set(prev)
      for (const p of proposals) {
        const sourceItemId = p.type === 'concept' ? p.payload.source_item_id : undefined
        if (!sourceItemId && !optimisticResolutions.has(p.id)) next.add(p.id)
      }
      return next
    })
  }

  // Cards still pending (not yet acted on)
  const selectablePendingCount = proposals.filter((p) => !optimisticResolutions.has(p.id)).length
  const allSelected = selectablePendingCount > 0 && selectedIds.size === selectablePendingCount
  const someSelected = selectedIds.size > 0 && !allSelected
  const selectedCount = selectedIds.size

  // --- Single-item actions ---

  function handleAccept(id: string) {
    setPendingId(id)
    setErrorId(null)
    resolveMutation.mutate(
      { id, action: 'accept' },
      {
        onSuccess: () => {
          setPendingId(null)
          setSelectedIds((prev) => { const n = new Set(prev); n.delete(id); return n })
          // Optimistically update card state — keep it visible in pending tab
          setOptimisticResolutions((prev) => {
            const next = new Map(prev)
            next.set(id, { status: 'accepted', resolvedAt: new Date().toISOString() })
            return next
          })
          addToast({ type: 'success', message: t('knowledge.proposals.acceptedToast') })
          // After 3 seconds, show "moved to Accepted tab" toast
          setTimeout(() => {
            addToast({ type: 'info', message: t('proposals.movedToAccepted') })
          }, 3000)
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
          setSelectedIds((prev) => { const n = new Set(prev); n.delete(id); return n })
          // Optimistically update card state — keep it visible in pending tab
          setOptimisticResolutions((prev) => {
            const next = new Map(prev)
            next.set(id, { status: 'rejected', resolvedAt: new Date().toISOString() })
            return next
          })
          addToast({ type: 'info', message: t('knowledge.proposals.rejectedToast') })
          // After 3 seconds, show "moved to Rejected tab" toast
          setTimeout(() => {
            addToast({ type: 'info', message: t('proposals.movedToRejected') })
          }, 3000)
        },
        onError: () => {
          setPendingId(null)
          setErrorId(id)
        },
      },
    )
  }

  // --- Batch actions ---

  function executeBatch(action: 'accept' | 'reject') {
    const ids = Array.from(selectedIds)
    batchMutation.mutate(
      { ids, action },
      {
        onSuccess: () => {
          const now = new Date().toISOString()
          setOptimisticResolutions((prev) => {
            const next = new Map(prev)
            for (const id of ids) {
              next.set(id, { status: action === 'accept' ? 'accepted' : 'rejected', resolvedAt: now })
            }
            return next
          })
          setSelectedIds(new Set())
          const msg = action === 'accept'
            ? t('knowledge.proposals.batchAcceptedToast', { count: ids.length, defaultValue: `${ids.length} proposals accepted` })
            : t('knowledge.proposals.batchRejectedToast', { count: ids.length, defaultValue: `${ids.length} proposals rejected` })
          addToast({ type: action === 'accept' ? 'success' : 'info', message: msg })
        },
        onError: () => {
          addToast({ type: 'error', message: t('error.loadFailed') })
        },
      },
    )
    setPendingBatchAction(null)
    batchDialogRef.current?.close()
  }

  function handleBatchAction(action: 'accept' | 'reject') {
    if (selectedCount >= 5) {
      setPendingBatchAction(action)
      batchDialogRef.current?.showModal()
    } else {
      executeBatch(action)
    }
  }

  function handleBatchDialogConfirm() {
    if (pendingBatchAction) executeBatch(pendingBatchAction)
  }

  function handleBatchDialogCancel() {
    setPendingBatchAction(null)
    batchDialogRef.current?.close()
  }

  // --- Source summary counts ---
  const articleCount = proposals.filter((p) => p.type === 'concept' && p.payload.source_item_type === 'article').length
  const agentCount = proposals.filter((p) => {
    if (p.type === 'concept' || p.type === 'knowledge') return !p.payload.source_item_id
    return false
  }).length

  const countLabel = isLoading
    ? '…'
    : t('knowledge.proposals.awaiting', { count: pendingCount })

  // Tab definitions
  const tabs: { key: TabStatus; labelKey: string; count: number | null }[] = [
    { key: 'pending', labelKey: 'proposals.tabs.pending', count: pendingCount },
    { key: 'accepted', labelKey: 'proposals.tabs.accepted', count: acceptedCount },
    { key: 'rejected', labelKey: 'proposals.tabs.rejected', count: rejectedCount },
    { key: 'all', labelKey: 'proposals.tabs.all', count: allCount },
  ]

  return (
    <section
      aria-labelledby="proposals-heading"
      className="rounded-lg p-5 mb-6"
      style={{
        background: 'var(--color-bg-card)',
        border: '1px solid var(--color-border)',
        position: 'relative',
      }}
    >
      {/* Section header */}
      <div className="flex items-center justify-between mb-3">
        <div className="flex items-center gap-2 flex-wrap">
          <h2
            id="proposals-heading"
            className="text-label"
            style={{ color: 'var(--color-text-muted)' }}
          >
            {t('knowledge.proposals.sectionTitle')}
          </h2>
          {!isLoading && !isError && activeTab === 'pending' && (
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
          {/* Source summary (only on pending tab) */}
          {!isLoading && !isError && activeTab === 'pending' && proposals.length > 0 && (
            <span
              className="text-caption"
              style={{ color: 'var(--color-text-muted)' }}
            >
              {articleCount > 0 && `${articleCount} from articles`}
              {articleCount > 0 && agentCount > 0 && ' / '}
              {agentCount > 0 && `${agentCount} agent-proposed`}
            </span>
          )}
        </div>
        <button
          type="button"
          onClick={() => setSectionCollapsed((c) => !c)}
          aria-expanded={!sectionCollapsed}
          aria-controls="proposals-list"
          aria-label={sectionCollapsed
            ? t('knowledge.proposals.expand', 'Expand proposals section')
            : t('knowledge.proposals.collapse', 'Collapse proposals section')}
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
          {sectionCollapsed
            ? <ChevronRight size={14} aria-hidden="true" />
            : <ChevronDown size={14} aria-hidden="true" />}
        </button>
      </div>

      {/* Status tabs */}
      <div
        role="tablist"
        aria-label="Proposal status tabs"
        className="flex gap-1 mb-4 flex-wrap"
      >
        {tabs.map((tab) => {
          const isActive = activeTab === tab.key
          return (
            <button
              key={tab.key}
              role="tab"
              type="button"
              aria-selected={isActive}
              onClick={() => setActiveTab(tab.key)}
              className="rounded-md px-3 py-1.5 text-body-sm transition-colors flex items-center gap-1.5"
              style={{
                background: isActive ? 'var(--color-accent-blue)' : 'var(--color-bg-hover)',
                color: isActive ? 'var(--color-bg-base)' : 'var(--color-text-muted)',
                border: 'none',
                cursor: 'pointer',
                fontWeight: isActive ? 600 : 400,
              }}
            >
              {t(tab.labelKey)}
              {tab.count !== null && (
                <span
                  className="text-label rounded-full px-1.5 py-0.5"
                  style={{
                    background: isActive ? 'rgba(0,0,0,0.2)' : 'var(--color-bg-input)',
                    color: isActive ? 'var(--color-bg-base)' : 'var(--color-text-muted)',
                    minWidth: '20px',
                    textAlign: 'center',
                  }}
                >
                  {tab.count}
                </span>
              )}
            </button>
          )
        })}
      </div>

      {/* Body */}
      {!sectionCollapsed && (
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
          ) : proposals.length === 0 ? (
            <div
              className="text-body-sm py-6 text-center"
              style={{ color: 'var(--color-text-muted)' }}
            >
              {activeTab === 'pending' ? t('knowledge.proposals.awaiting', { count: 0 }) : t('common.empty')}
            </div>
          ) : (
            <>
              {/* Batch toolbar — only show in pending tab */}
              {activeTab === 'pending' && (
                <div className="flex items-center gap-3 mb-4 flex-wrap">
                  {/* Select all checkbox */}
                  <label className="flex items-center gap-2 text-body-sm" style={{ color: 'var(--color-text-muted)', cursor: 'pointer' }}>
                    <input
                      type="checkbox"
                      checked={allSelected}
                      ref={(el) => {
                        if (el) el.indeterminate = someSelected
                      }}
                      onChange={toggleSelectAll}
                      aria-label="Select all proposals"
                      style={{
                        width: '16px',
                        height: '16px',
                        cursor: 'pointer',
                        accentColor: 'var(--color-accent-blue)',
                      }}
                    />
                    Select all
                  </label>

                  {/* Quick filters */}
                  <button
                    type="button"
                    onClick={() => selectAllOfType('concept')}
                    className="rounded px-2 py-0.5 text-label transition-opacity hover:opacity-80"
                    style={{
                      background: 'var(--color-bg-hover)',
                      border: '1px solid var(--color-border)',
                      color: 'var(--color-text-muted)',
                      cursor: 'pointer',
                    }}
                  >
                    All concepts
                  </button>
                  <button
                    type="button"
                    onClick={selectAllAutoSource}
                    className="rounded px-2 py-0.5 text-label transition-opacity hover:opacity-80"
                    style={{
                      background: 'var(--color-bg-hover)',
                      border: '1px solid var(--color-border)',
                      color: 'var(--color-text-muted)',
                      cursor: 'pointer',
                    }}
                  >
                    All agent-proposed
                  </button>
                </div>
              )}

              {/* Groups by type */}
              {TYPE_ORDER.map((type) => {
                const group = grouped[type]
                if (group.length === 0) return null
                const isGroupCollapsed = groupState[type].collapsed

                return (
                  <div key={type} className="mb-4">
                    {/* Group header */}
                    <button
                      type="button"
                      onClick={() =>
                        setGroupState((prev) => ({
                          ...prev,
                          [type]: { collapsed: !prev[type].collapsed },
                        }))
                      }
                      aria-expanded={!isGroupCollapsed}
                      className="flex items-center gap-2 mb-2 w-full text-left"
                      style={{
                        background: 'transparent',
                        border: 'none',
                        cursor: 'pointer',
                        padding: '0',
                      }}
                    >
                      {isGroupCollapsed
                        ? <ChevronRight size={12} aria-hidden="true" style={{ color: 'var(--color-text-muted)' }} />
                        : <ChevronDown size={12} aria-hidden="true" style={{ color: 'var(--color-text-muted)' }} />}
                      <span
                        className="text-label"
                        style={{ color: 'var(--color-text-muted)' }}
                      >
                        {TYPE_LABELS[type]}
                      </span>
                      <span
                        className="text-label rounded-full px-2 py-0.5"
                        style={{
                          background: 'var(--color-bg-hover)',
                          color: 'var(--color-text-muted)',
                        }}
                      >
                        {group.length}
                      </span>
                    </button>

                    {/* Group cards */}
                    {!isGroupCollapsed && group.map((p) => {
                      const optimistic = optimisticResolutions.get(p.id)
                      return (
                        <PendingProposalCard
                          key={p.id}
                          proposal={p}
                          onAccept={handleAccept}
                          onReject={handleReject}
                          isPending={pendingId === p.id}
                          error={errorId === p.id}
                          selected={selectedIds.has(p.id)}
                          onSelectChange={toggleSelect}
                          optimisticStatus={optimistic?.status ?? null}
                          optimisticResolvedAt={optimistic?.resolvedAt ?? null}
                        />
                      )
                    })}
                  </div>
                )
              })}
            </>
          )}
        </div>
      )}

      {/* Footer sticky bar — shown when items are selected */}
      {selectedCount > 0 && !sectionCollapsed && !isLoading && !isError && activeTab === 'pending' && (
        <div
          className="sticky bottom-0 flex items-center justify-between gap-3 mt-4 rounded-md px-4 py-3"
          style={{
            background: 'var(--color-bg-overlay)',
            border: '1px solid var(--color-border)',
            backdropFilter: 'blur(8px)',
          }}
          aria-live="polite"
          aria-atomic="true"
        >
          <span className="text-body-sm" style={{ color: 'var(--color-text-muted)' }}>
            {selectedCount} selected
          </span>
          <div className="flex gap-2">
            <button
              type="button"
              onClick={() => handleBatchAction('reject')}
              disabled={batchMutation.isPending}
              className="rounded-md px-4 py-2 text-body-sm transition-opacity"
              style={{
                background: 'transparent',
                border: '1px solid var(--color-error)',
                color: 'var(--color-error)',
                cursor: batchMutation.isPending ? 'not-allowed' : 'pointer',
                opacity: batchMutation.isPending ? 0.6 : 1,
              }}
            >
              Reject {selectedCount}
            </button>
            <button
              type="button"
              onClick={() => handleBatchAction('accept')}
              disabled={batchMutation.isPending}
              className="rounded-md px-4 py-2 text-body-sm transition-opacity"
              style={{
                background: 'var(--color-accent-blue)',
                color: 'var(--color-bg-base)',
                border: 'none',
                cursor: batchMutation.isPending ? 'not-allowed' : 'pointer',
                opacity: batchMutation.isPending ? 0.6 : 1,
              }}
            >
              Accept {selectedCount}
            </button>
          </div>
        </div>
      )}

      {/* Batch confirm dialog (shown when selecting >= 5 items) */}
      <dialog
        ref={batchDialogRef}
        aria-labelledby="batch-dialog-title"
        aria-describedby="batch-dialog-desc"
        style={{ width: '100%', maxWidth: '400px' }}
      >
        <div
          className="rounded-lg p-5"
          style={{
            background: 'var(--color-bg-card)',
            border: '1px solid var(--color-border)',
          }}
        >
          <h3
            id="batch-dialog-title"
            className="text-card-title mb-2"
            style={{ color: 'var(--color-text-primary)' }}
          >
            {pendingBatchAction === 'accept'
              ? `Accept ${selectedCount} proposals?`
              : `Reject ${selectedCount} proposals?`}
          </h3>
          <p
            id="batch-dialog-desc"
            className="text-body-sm mb-4"
            style={{ color: 'var(--color-text-muted)' }}
          >
            {pendingBatchAction === 'accept'
              ? 'All selected proposals will be accepted and added to the learning queue.'
              : 'All selected proposals will be permanently rejected.'}
          </p>
          <div className="flex justify-end gap-2">
            <button
              type="button"
              onClick={handleBatchDialogCancel}
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
              onClick={handleBatchDialogConfirm}
              className="rounded-md px-4 py-2 text-body-sm"
              style={{
                background: pendingBatchAction === 'accept'
                  ? 'var(--color-accent-blue)'
                  : 'var(--color-error)',
                color: pendingBatchAction === 'accept' ? 'var(--color-bg-base)' : '#fff',
                border: 'none',
                cursor: 'pointer',
              }}
            >
              {pendingBatchAction === 'accept' ? 'Accept all' : 'Reject all'}
            </button>
          </div>
        </div>
      </dialog>
    </section>
  )
}
