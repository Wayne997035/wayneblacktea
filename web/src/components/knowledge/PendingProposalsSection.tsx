import { useState, useRef } from 'react'
import { useTranslation } from 'react-i18next'
import { ChevronDown, ChevronRight } from 'lucide-react'
import { usePendingProposals } from '../../hooks/usePendingProposals'
import { useResolveProposal } from '../../hooks/useResolveProposal'
import { useConfirmBatchProposals } from '../../hooks/useConfirmBatchProposals'
import { useToastStore } from '../../stores/toastStore'
import { LoadingSkeleton } from '../ui/LoadingSkeleton'
import { PendingProposalCard } from './PendingProposalCard'
import type { PendingProposal, ProposalType } from '../../types/api'

// ----- types -----

interface GroupState {
  collapsed: boolean
}

// ----- helpers -----

const TYPE_ORDER: ProposalType[] = ['goal', 'project', 'task', 'concept']
const TYPE_LABELS: Record<ProposalType, string> = {
  goal: 'Goals',
  project: 'Projects',
  task: 'Tasks',
  concept: 'Concepts',
}

function groupByType(proposals: PendingProposal[]): Record<ProposalType, PendingProposal[]> {
  const result: Record<ProposalType, PendingProposal[]> = {
    goal: [], project: [], task: [], concept: [],
  }
  for (const p of proposals) {
    result[p.type].push(p)
  }
  return result
}

// ----- component -----

export function PendingProposalsSection() {
  const { t } = useTranslation()
  const { addToast } = useToastStore()

  // Section collapse
  const [sectionCollapsed, setSectionCollapsed] = useState(false)

  // Per-type group collapse state
  const [groupState, setGroupState] = useState<Record<ProposalType, GroupState>>({
    goal: { collapsed: false },
    project: { collapsed: false },
    task: { collapsed: false },
    concept: { collapsed: false },
  })

  // Single-item pending state
  const [pendingId, setPendingId] = useState<string | null>(null)
  const [errorId, setErrorId] = useState<string | null>(null)

  // Batch selection
  const [selectedIds, setSelectedIds] = useState<Set<string>>(new Set())

  // Batch confirm dialog (for batch >= 5)
  const batchDialogRef = useRef<HTMLDialogElement>(null)
  const [pendingBatchAction, setPendingBatchAction] = useState<'accept' | 'reject' | null>(null)

  const { data: proposals = [], isLoading, isError, refetch } = usePendingProposals()
  const resolveMutation = useResolveProposal()
  const batchMutation = useConfirmBatchProposals()

  // Only render when there's something to show
  if (!isLoading && !isError && proposals.length === 0) {
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
    if (selectedIds.size === proposals.length) {
      setSelectedIds(new Set())
    } else {
      setSelectedIds(new Set(proposals.map((p) => p.id)))
    }
  }

  function selectAllOfType(type: ProposalType) {
    setSelectedIds((prev) => {
      const next = new Set(prev)
      for (const p of grouped[type]) next.add(p.id)
      return next
    })
  }

  function selectAllAutoSource() {
    setSelectedIds((prev) => {
      const next = new Set(prev)
      for (const p of proposals) {
        if (!p.payload.source_item_id) next.add(p.id) // agent-proposed
      }
      return next
    })
  }

  const allSelected = proposals.length > 0 && selectedIds.size === proposals.length
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
          setSelectedIds((prev) => { const n = new Set(prev); n.delete(id); return n })
          addToast({ type: 'info', message: t('knowledge.proposals.rejectedToast') })
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
  const articleCount = proposals.filter((p) => p.payload.source_item_type === 'article').length
  const agentCount = proposals.filter((p) => !p.payload.source_item_id).length

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
          {/* Source summary */}
          {!isLoading && !isError && proposals.length > 0 && (
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
          ) : (
            <>
              {/* Batch toolbar */}
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
                    {!isGroupCollapsed && group.map((p) => (
                      <PendingProposalCard
                        key={p.id}
                        proposal={p}
                        onAccept={handleAccept}
                        onReject={handleReject}
                        isPending={pendingId === p.id}
                        error={errorId === p.id}
                        selected={selectedIds.has(p.id)}
                        onSelectChange={toggleSelect}
                      />
                    ))}
                  </div>
                )
              })}
            </>
          )}
        </div>
      )}

      {/* Footer sticky bar — shown when items are selected */}
      {selectedCount > 0 && !sectionCollapsed && !isLoading && !isError && (
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
