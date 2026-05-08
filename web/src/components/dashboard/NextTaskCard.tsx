import { CheckSquare } from 'lucide-react'
import { LoadingSkeleton } from '../ui/LoadingSkeleton'
import { EmptyState } from '../ui/EmptyState'
import { useDashboardNextTask } from '../../hooks/useDashboardNextTask'

interface NextTaskCardProps {
  onNavigate: (taskId: string) => void
}

interface PriorityStyle {
  label: string
  bg: string
  color: string
  border: string
  leftBorder: string
}

function getPriorityStyle(priority: number | null): PriorityStyle | null {
  if (priority === null) return null
  if (priority === 1) {
    return {
      label: 'P1',
      bg: '#2e0a0a',
      color: 'var(--color-error)',
      border: 'var(--color-error)',
      leftBorder: 'var(--color-error)',
    }
  }
  if (priority === 2) {
    return {
      label: 'P2',
      bg: '#2e1f00',
      color: 'var(--color-warning)',
      border: 'var(--color-warning)',
      leftBorder: 'var(--color-warning)',
    }
  }
  return {
    label: `P${priority}+`,
    bg: '#0a1f35',
    color: 'var(--color-accent-blue)',
    border: 'var(--color-accent-blue)',
    leftBorder: 'var(--color-accent-blue)',
  }
}

function formatDueDate(due: string): string {
  return new Date(due).toLocaleDateString('en', { month: 'short', day: 'numeric' })
}

export function NextTaskCard({ onNavigate }: NextTaskCardProps) {
  const { data, isLoading } = useDashboardNextTask()

  // Loading state
  if (isLoading) {
    return (
      <div
        className="rounded-lg p-4"
        style={{
          background: 'var(--color-bg-card)',
          border: '1px solid var(--color-border)',
          minHeight: '76px',
        }}
      >
        <LoadingSkeleton style={{ width: '60px', height: '14px' }} />
        <div className="mt-3">
          <LoadingSkeleton style={{ width: '100%', height: '16px' }} />
          <div className="flex items-center justify-between mt-2">
            <LoadingSkeleton style={{ width: '80%', height: '12px' }} />
            <LoadingSkeleton style={{ width: '50px', height: '12px' }} />
          </div>
        </div>
      </div>
    )
  }

  const task = data?.task ?? null

  // Empty state
  if (!task) {
    return (
      <div
        className="rounded-lg p-4"
        style={{
          background: 'var(--color-bg-card)',
          border: '1px solid var(--color-border)',
          minHeight: '76px',
        }}
      >
        <EmptyState messageKey="dashboard.noNextTask" />
      </div>
    )
  }

  const priorityStyle = getPriorityStyle(task.priority)

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === 'Enter' || e.key === ' ') {
      e.preventDefault()
      onNavigate(task.id)
    }
  }

  const priorityLabel = priorityStyle?.label ?? ''

  return (
    <article
      role="button"
      tabIndex={0}
      aria-label={`Next task: ${task.title}${priorityLabel ? `, priority ${priorityLabel}` : ''}`}
      className="rounded-lg p-4"
      style={{
        background: 'var(--color-bg-card)',
        border: '1px solid var(--color-border)',
        borderLeft: priorityStyle ? `4px solid ${priorityStyle.leftBorder}` : '1px solid var(--color-border)',
        cursor: 'pointer',
        minHeight: '76px',
      }}
      onClick={() => onNavigate(task.id)}
      onKeyDown={handleKeyDown}
      onMouseEnter={(e) => {
        e.currentTarget.style.background = 'var(--color-bg-hover)'
      }}
      onMouseLeave={(e) => {
        e.currentTarget.style.background = 'var(--color-bg-card)'
      }}
    >
      <div className="flex items-center gap-2 mb-2">
        <CheckSquare size={16} aria-hidden="true" style={{ color: 'var(--color-accent-blue)' }} />
        <span className="text-label" style={{ color: 'var(--color-text-muted)' }}>
          Next Task
        </span>
        {priorityStyle && (
          <span
            className="ml-auto rounded font-mono text-caption px-1.5 py-0.5"
            style={{
              background: priorityStyle.bg,
              color: priorityStyle.color,
              border: `1px solid ${priorityStyle.border}`,
            }}
          >
            {priorityStyle.label}
          </span>
        )}
      </div>

      <p className="text-card-title mb-1" style={{ color: 'var(--color-text-primary)' }}>
        {task.title}
      </p>

      {task.due_date && (
        <p className="text-caption" style={{ color: 'var(--color-text-muted)' }}>
          Due: {formatDueDate(task.due_date)}
        </p>
      )}
    </article>
  )
}
