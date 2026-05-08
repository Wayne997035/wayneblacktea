import { LoadingSkeleton } from '../ui/LoadingSkeleton'
import { EmptyState } from '../ui/EmptyState'
import { usePendingProposals } from '../../hooks/usePendingProposals'
import type { ProposalType } from '../../types/api'

interface PendingProposalsCardProps {
  onClick: () => void
}

interface ChipStyle {
  color: string
  bg: string
}

function getChipStyle(type: ProposalType): ChipStyle {
  switch (type) {
    case 'concept':
      return { color: 'var(--color-accent-blue)', bg: 'var(--color-bg-hover)' }
    case 'goal':
      return { color: 'var(--color-success)', bg: '#0a2e0a' }
    case 'project':
      return { color: 'var(--color-accent-purple)', bg: '#1a0a35' }
    case 'task':
      return { color: 'var(--color-warning)', bg: '#2e1f00' }
  }
}

export function PendingProposalsCard({ onClick }: PendingProposalsCardProps) {
  const { data, isLoading } = usePendingProposals()

  if (isLoading) {
    return (
      <div
        className="rounded-lg p-4"
        style={{
          background: 'var(--color-bg-card)',
          border: '1px solid var(--color-border)',
        }}
      >
        <div className="flex items-center justify-between mb-2">
          <LoadingSkeleton style={{ width: '50%', height: '14px' }} />
          <LoadingSkeleton style={{ width: '24px', height: '24px' }} />
        </div>
        <LoadingSkeleton style={{ width: '80%', height: '12px' }} />
        <div className="mt-2">
          <LoadingSkeleton style={{ width: '60%', height: '12px' }} />
        </div>
      </div>
    )
  }

  const proposals = data ?? []
  const count = proposals.length

  if (count === 0) {
    return (
      <div
        className="rounded-lg p-4"
        style={{
          background: 'var(--color-bg-card)',
          border: '1px solid var(--color-border)',
        }}
      >
        <div className="text-label mb-2" style={{ color: 'var(--color-text-muted)' }}>
          PENDING PROPOSALS
        </div>
        <EmptyState messageKey="dashboard.noHandoff" />
      </div>
    )
  }

  const countLabel = count >= 10 ? '10+' : String(count)
  const visibleChips = proposals.slice(0, 3)

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === 'Enter' || e.key === ' ') {
      e.preventDefault()
      onClick()
    }
  }

  return (
    <article
      role="button"
      tabIndex={0}
      aria-label={`Pending proposals: ${count} awaiting review`}
      className="rounded-lg p-4 transition-colors"
      style={{
        background: 'var(--color-bg-card)',
        border: '1px solid var(--color-border)',
        borderLeft: '4px solid var(--color-warning)',
        cursor: 'pointer',
      }}
      onClick={onClick}
      onKeyDown={handleKeyDown}
      onMouseEnter={(e) => {
        e.currentTarget.style.background = 'var(--color-bg-hover)'
      }}
      onMouseLeave={(e) => {
        e.currentTarget.style.background = 'var(--color-bg-card)'
      }}
    >
      <div className="flex items-center justify-between">
        <span className="text-label" style={{ color: 'var(--color-text-muted)' }}>
          PENDING PROPOSALS
        </span>
        <span
          className="rounded-full font-mono text-caption px-2 py-0.5"
          aria-label={`${count} pending proposals`}
          style={{
            background: '#2e1f00',
            color: 'var(--color-warning)',
            border: '1px solid var(--color-warning)',
          }}
        >
          {countLabel}
        </span>
      </div>

      <div className="flex items-center gap-2 mt-3 flex-wrap">
        {visibleChips.map((proposal) => {
          const style = getChipStyle(proposal.type)
          return (
            <span
              key={proposal.id}
              aria-hidden="true"
              className="rounded text-caption px-1.5 py-0.5"
              style={{ background: style.bg, color: style.color }}
            >
              {proposal.type}
            </span>
          )
        })}
      </div>
    </article>
  )
}
