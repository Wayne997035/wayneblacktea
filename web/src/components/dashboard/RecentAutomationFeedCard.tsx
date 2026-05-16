import { LoadingSkeleton } from '../ui/LoadingSkeleton'
import { EmptyState } from '../ui/EmptyState'
import { useAutomationFeed } from '../../hooks/useAutomationFeed'
import { formatRelative } from '../../lib/formatRelative'
import type { AutomationFeedItem } from '../../types/api'

/** Returns a CSS colour string for the action kind dot. */
function kindDotColor(kind: string): string {
  switch (kind) {
    case 'task_completed':
      return 'var(--color-success)'
    case 'task_created':
      return 'var(--color-accent-blue)'
    case 'decision':
      return 'var(--color-accent-purple)'
    case 'handoff_created':
    case 'handoff_resolved':
      return 'var(--color-text-muted)'
    case 'activity':
    default:
      if (kind.startsWith('dashboard')) return 'var(--color-warning)'
      return 'var(--color-text-muted)'
  }
}

interface FeedRowProps {
  item: AutomationFeedItem
}

function FeedRow({ item }: FeedRowProps) {
  const color = kindDotColor(item.kind)
  return (
    <div className="flex items-start gap-3 py-2" style={{ borderTop: '1px solid var(--color-border)' }}>
      <span
        aria-hidden="true"
        className="mt-1 w-2 h-2 rounded-full shrink-0"
        style={{ background: color }}
      />
      <div className="flex-1 min-w-0">
        <span
          className="text-body-sm block truncate"
          style={{ color: 'var(--color-text-primary)' }}
          title={item.notes ?? item.action}
        >
          {item.notes ? item.notes : item.action}
        </span>
        <span className="text-caption" style={{ color: 'var(--color-text-muted)' }}>
          {item.action} · {formatRelative(item.occurred_at)}
        </span>
      </div>
    </div>
  )
}

export function RecentAutomationFeedCard() {
  const { data, isLoading, isError } = useAutomationFeed(10)

  if (isLoading) {
    return (
      <div
        className="rounded-lg p-4"
        style={{
          background: 'var(--color-bg-card)',
          border: '1px solid var(--color-border)',
        }}
      >
        <LoadingSkeleton style={{ width: '60%', height: '14px', marginBottom: '12px' }} />
        {Array.from({ length: 3 }, (_, i) => (
          <div key={i} className="flex items-start gap-3 py-2" style={{ borderTop: '1px solid var(--color-border)' }}>
            <LoadingSkeleton style={{ width: '8px', height: '8px', borderRadius: '50%', marginTop: '4px', flexShrink: 0 }} />
            <div style={{ flex: 1 }}>
              <LoadingSkeleton style={{ width: `${60 + i * 10}%`, height: '12px', marginBottom: '4px' }} />
              <LoadingSkeleton style={{ width: '40%', height: '10px' }} />
            </div>
          </div>
        ))}
      </div>
    )
  }

  if (isError) {
    return (
      <div
        className="rounded-lg p-4"
        style={{
          background: 'var(--color-bg-card)',
          border: '1px solid var(--color-border)',
        }}
      >
        <div className="text-label mb-2" style={{ color: 'var(--color-text-muted)' }}>
          MCP Automation Feed
        </div>
        <EmptyState messageKey="error.loadFailed" />
      </div>
    )
  }

  const feed = data?.feed ?? []
  const fetchedAt = data?.fetched_at

  return (
    <div
      className="rounded-lg"
      style={{
        background: 'var(--color-bg-card)',
        border: '1px solid var(--color-border)',
      }}
    >
      <div className="px-4 pt-4 pb-2 flex items-center justify-between">
        <span className="text-label" style={{ color: 'var(--color-text-muted)' }}>
          MCP Automation Feed
        </span>
        {fetchedAt && (
          <span className="text-caption" style={{ color: 'var(--color-text-muted)' }}>
            Last refreshed {formatRelative(fetchedAt)}
          </span>
        )}
      </div>

      {feed.length === 0 ? (
        <div style={{ borderTop: '1px solid var(--color-border)' }}>
          <EmptyState messageKey="dashboard.noMcpActions" />
        </div>
      ) : (
        <div className="px-4 pb-3">
          {feed.map((item) => (
            <FeedRow key={item.id} item={item} />
          ))}
        </div>
      )}
    </div>
  )
}
