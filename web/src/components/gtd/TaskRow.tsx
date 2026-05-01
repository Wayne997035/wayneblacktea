import { useState } from 'react'
import { ChevronDown, ChevronRight } from 'lucide-react'
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
  if (status === 'completed') return 'completed'
  if (status === 'cancelled') return 'archived'
  if (status === 'in_progress') return 'active'
  return 'on_hold' // pending
}

const PRIORITY_LABELS: Record<number, string> = {
  1: 'Low',
  2: 'Normal',
  3: 'Medium',
  4: 'High',
  5: 'Critical',
}

export function TaskRow({ task, onComplete }: TaskRowProps) {
  const isCompleted = task.status === 'completed'
  const [isExpanded, setIsExpanded] = useState(false)
  const expandId = `task-detail-${task.id}`
  const hasDetails = Boolean(task.description || task.assignee)

  return (
    <li
      style={{ borderBottom: '1px solid var(--color-border)' }}
    >
      {/* Main row */}
      <div
        className="flex items-center gap-3 py-3 px-4 transition-colors min-h-[44px]"
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

          {/* Expand toggle */}
          <button
            type="button"
            onClick={() => setIsExpanded((v) => !v)}
            aria-expanded={isExpanded}
            aria-controls={expandId}
            aria-label={isExpanded ? 'Collapse task details' : 'Expand task details'}
            className="flex items-center justify-center w-6 h-6 rounded transition-colors"
            style={{
              background: 'transparent',
              border: 'none',
              cursor: 'pointer',
              color: hasDetails ? 'var(--color-text-muted)' : 'var(--color-text-disabled)',
            }}
          >
            {isExpanded
              ? <ChevronDown size={14} aria-hidden="true" />
              : <ChevronRight size={14} aria-hidden="true" />
            }
          </button>
        </div>
      </div>

      {/* Expanded detail panel */}
      {isExpanded && (
        <div
          id={expandId}
          className="px-4 pb-3 pt-1"
          style={{
            background: 'var(--color-bg-hover)',
            borderTop: '1px solid var(--color-border)',
          }}
        >
          <div className="flex flex-wrap gap-x-6 gap-y-2 text-body-sm">
            {/* Priority */}
            <div>
              <span className="text-label" style={{ color: 'var(--color-text-muted)' }}>
                Priority
              </span>
              <span
                className="ml-2"
                style={{ color: 'var(--color-text-primary)' }}
              >
                {PRIORITY_LABELS[task.priority] ?? task.priority}
              </span>
            </div>

            {/* Assignee */}
            {task.assignee && (
              <div>
                <span className="text-label" style={{ color: 'var(--color-text-muted)' }}>
                  Assignee
                </span>
                <span className="ml-2" style={{ color: 'var(--color-text-primary)' }}>
                  {task.assignee}
                </span>
              </div>
            )}

            {/* Due date (full) */}
            {task.due_date && (
              <div>
                <span className="text-label" style={{ color: 'var(--color-text-muted)' }}>
                  Due
                </span>
                <span
                  className="ml-2"
                  style={{
                    color: isPastDue(task.due_date) ? 'var(--color-error)' : 'var(--color-text-primary)',
                  }}
                >
                  {new Date(task.due_date).toLocaleDateString(undefined, {
                    year: 'numeric',
                    month: 'short',
                    day: 'numeric',
                  })}
                </span>
              </div>
            )}

            {/* Status */}
            <div>
              <span className="text-label" style={{ color: 'var(--color-text-muted)' }}>
                Status
              </span>
              <span className="ml-2" style={{ color: 'var(--color-text-primary)' }}>
                {task.status.replace('_', ' ')}
              </span>
            </div>
          </div>

          {/* Description */}
          {task.description && (
            <p
              className="text-body-sm mt-2"
              style={{ color: 'var(--color-text-muted)', whiteSpace: 'pre-wrap' }}
            >
              {task.description}
            </p>
          )}

          {/* Artifact link */}
          {task.artifact && (
            <div className="mt-2">
              <span className="text-label" style={{ color: 'var(--color-text-muted)' }}>
                Artifact
              </span>
              <a
                href={task.artifact}
                target="_blank"
                rel="noopener noreferrer"
                className="ml-2 text-body-sm underline underline-offset-2"
                style={{ color: 'var(--color-accent-blue)' }}
              >
                {task.artifact}
              </a>
            </div>
          )}
        </div>
      )}
    </li>
  )
}
