import { PriorityDot } from '../ui/PriorityDot'
import { StatusBadge } from '../ui/StatusBadge'
import type { Task, ProjectStatus } from '../../types/api'

interface TaskRowProps {
  task: Task;
  onComplete?: (id: string) => void;
  onStatusChange?: (id: string, status: string) => void;
}

function formatDueDate(dateStr: string): string {
  return new Date(dateStr).toLocaleDateString(undefined, { month: 'short', day: 'numeric' })
}

function isPastDue(dateStr: string): boolean {
  return new Date(dateStr) < new Date()
}

// Map task status to badge-compatible status (for display)
function toDisplayStatus(status: string): ProjectStatus {
  if (status === 'done') return 'completed'
  if (status === 'in_progress') return 'active'
  if (status === 'blocked') return 'on_hold'
  return 'active'
}

export function TaskRow({ task, onComplete }: TaskRowProps) {
  const isCompleted = task.status === 'done'

  return (
    <li
      className="flex items-center gap-3 py-3 px-4 transition-colors min-h-[44px]"
      style={{
        borderBottom: '1px solid var(--color-border)',
      }}
      onMouseEnter={(e) => { e.currentTarget.style.background = 'var(--color-bg-hover)' }}
      onMouseLeave={(e) => { e.currentTarget.style.background = 'transparent' }}
    >
      {/* Checkbox */}
      <label className="shrink-0 cursor-pointer">
        <input
          type="checkbox"
          checked={isCompleted}
          onChange={() => onComplete?.(task.id)}
          aria-label={`Complete: ${task.title}`}
          className="sr-only"
        />
        <span
          className="flex items-center justify-center w-[18px] h-[18px] rounded-sm"
          style={{
            border: isCompleted ? 'none' : '1px solid var(--color-border)',
            background: isCompleted ? 'var(--color-accent-blue)' : 'var(--color-bg-input)',
          }}
          aria-hidden="true"
        >
          {isCompleted && (
            <svg width="12" height="9" viewBox="0 0 12 9" fill="none">
              <path d="M1 4L4.5 7.5L11 1" stroke="var(--color-bg-base)" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" />
            </svg>
          )}
        </span>
      </label>

      <PriorityDot level={task.priority} />

      <span
        className="text-body flex-1 truncate"
        style={{
          color: isCompleted ? 'var(--color-text-muted)' : 'var(--color-text-primary)',
          textDecoration: isCompleted ? 'line-through' : 'none',
          opacity: isCompleted ? 0.7 : 1,
        }}
      >
        {task.title}
      </span>

      <div className="flex items-center gap-2 shrink-0">
        {task.due_date && (
          <span
            className="text-caption"
            style={{ color: isPastDue(task.due_date) ? 'var(--color-error)' : 'var(--color-text-muted)' }}
          >
            {formatDueDate(task.due_date)}
          </span>
        )}
        <StatusBadge status={toDisplayStatus(task.status)} size="sm" />
      </div>
    </li>
  )
}
