import { Pencil } from 'lucide-react'
import type { Goal } from '../../types/api'

interface GoalCardProps {
  goal: Goal;
  completedTasks: number;
  totalTasks: number;
  onEdit?: (goal: Goal) => void;
}

function getDaysLeft(dueDateStr: string): { label: string; color: string } {
  const due = new Date(dueDateStr)
  const now = new Date()
  const diffMs = due.getTime() - now.getTime()
  const diffDays = Math.ceil(diffMs / (1000 * 60 * 60 * 24))

  if (diffDays < 0) {
    return { label: `${Math.abs(diffDays)}d overdue`, color: 'var(--color-error)' }
  }
  if (diffDays < 7) {
    return { label: `${diffDays}d left`, color: 'var(--color-warning)' }
  }
  return { label: `${diffDays}d left`, color: 'var(--color-text-muted)' }
}

export function GoalCard({ goal, completedTasks, totalTasks, onEdit }: GoalCardProps) {
  const pct = totalTasks > 0 ? (completedTasks / totalTasks) * 100 : 0
  const daysLeft = goal.due_date ? getDaysLeft(goal.due_date) : null

  return (
    <article
      className="rounded-lg p-4"
      style={{
        background: 'var(--color-bg-card)',
        border: '1px solid var(--color-border)',
      }}
    >
      <div className="flex items-start justify-between gap-2 mb-1">
        <div className="min-w-0 flex-1">
          {goal.area && (
            <div className="text-label mb-1" style={{ color: 'var(--color-text-muted)' }}>
              {goal.area}
            </div>
          )}
          <h3 className="text-card-title" style={{ color: 'var(--color-text-primary)' }}>
            {goal.title}
          </h3>
        </div>
        {onEdit && (
          <button
            type="button"
            onClick={() => onEdit(goal)}
            aria-label="Edit goal"
            className="flex items-center justify-center w-7 h-7 rounded-md flex-shrink-0 transition-colors"
            style={{ color: 'var(--color-text-muted)', background: 'transparent', border: 'none', cursor: 'pointer' }}
            onMouseEnter={(e) => { e.currentTarget.style.background = 'var(--color-bg-hover)' }}
            onMouseLeave={(e) => { e.currentTarget.style.background = 'transparent' }}
          >
            <Pencil size={14} aria-hidden="true" />
          </button>
        )}
      </div>
      {goal.description && (
        <p
          className="text-body-sm mb-3"
          style={{
            color: 'var(--color-text-muted)',
            display: '-webkit-box',
            WebkitLineClamp: 2,
            WebkitBoxOrient: 'vertical',
            overflow: 'hidden',
          }}
        >
          {goal.description}
        </p>
      )}

      <div
        className="h-1.5 rounded-full mb-2"
        style={{ background: 'var(--color-border)', overflow: 'hidden' }}
        aria-label={`Progress: ${completedTasks} of ${totalTasks} tasks`}
      >
        <div
          className="h-full rounded-full"
          style={{
            width: `${pct}%`,
            background: 'var(--color-accent-blue)',
            transition: 'width 600ms ease-out',
          }}
        />
      </div>

      <div className="flex items-center justify-between">
        <span className="text-caption" style={{ color: 'var(--color-text-muted)' }}>
          {completedTasks} / {totalTasks}
        </span>
        {daysLeft && (
          <span className="text-caption" style={{ color: daysLeft.color }}>
            {daysLeft.label}
          </span>
        )}
      </div>
    </article>
  )
}
