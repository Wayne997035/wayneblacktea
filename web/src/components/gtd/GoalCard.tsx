import type { Goal } from '../../types/api'

interface GoalCardProps {
  goal: Goal;
  completedTasks: number;
  totalTasks: number;
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

export function GoalCard({ goal, completedTasks, totalTasks }: GoalCardProps) {
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
      {goal.area && (
        <div className="text-label mb-1" style={{ color: 'var(--color-text-muted)' }}>
          {goal.area}
        </div>
      )}
      <h3 className="text-card-title mb-1" style={{ color: 'var(--color-text-primary)' }}>
        {goal.title}
      </h3>
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
