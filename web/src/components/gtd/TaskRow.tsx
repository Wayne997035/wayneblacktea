import { useMemo } from 'react'
import { ChevronDown } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { PriorityDot } from '../ui/PriorityDot'
import { StatusBadge } from '../ui/StatusBadge'
import { LoadingSkeleton } from '../ui/LoadingSkeleton'
import { ImportanceBadge } from './ImportanceBadge'
import { useDecisions } from '../../hooks/useDecisions'
import type { Task, Project, ProjectStatus } from '../../types/api'

interface TaskRowProps {
  task: Task
  project?: Project
  expanded: boolean
  onToggle: () => void
  onComplete?: (id: string) => void
}

function formatDueDate(dateStr: string): string {
  return new Date(dateStr).toLocaleDateString(undefined, { month: 'short', day: 'numeric' })
}

function isPastDue(dateStr: string): boolean {
  return new Date(dateStr) < new Date()
}

function toDisplayStatus(status: string): ProjectStatus {
  if (status === 'completed') return 'completed'
  if (status === 'cancelled') return 'archived'
  if (status === 'in_progress') return 'active'
  return 'on_hold'
}

function toImportance(priority: 1 | 2 | 3 | 4 | 5): 1 | 2 | 3 {
  if (priority >= 4) return 1
  if (priority === 3) return 2
  return 3
}

function formatDecisionDate(dateStr: string): string {
  return new Date(dateStr).toLocaleDateString(undefined, { year: 'numeric', month: '2-digit', day: '2-digit' })
}

interface RecentDecisionsProps {
  projectId: string
  isExpanded: boolean
}

function RecentDecisions({ projectId, isExpanded }: RecentDecisionsProps) {
  const { t } = useTranslation()
  const { data: decisions, isLoading, isError } = useDecisions(projectId, { enabled: isExpanded && !!projectId })

  const recent = useMemo(() => {
    if (!decisions) return []
    return [...decisions]
      .sort((a, b) => b.created_at.localeCompare(a.created_at))
      .slice(0, 3)
  }, [decisions])

  if (!isExpanded) return null

  return (
    <div>
      <p className="text-label mb-1.5" style={{ color: 'var(--color-text-muted)' }}>
        {t('gtd.recentDecisions')}
        {!isLoading && !isError && ` (${recent.length})`}
      </p>

      {isLoading && (
        <div className="space-y-2">
          <LoadingSkeleton className="h-5 w-full" />
          <LoadingSkeleton className="h-5 w-4/5" />
        </div>
      )}

      {isError && (
        <p className="text-body-sm" style={{ color: 'var(--color-text-muted)' }}>
          {t('error.loadFailed')}
        </p>
      )}

      {!isLoading && !isError && recent.length === 0 && (
        <p className="text-body-sm" style={{ color: 'var(--color-text-muted)' }}>
          {t('gtd.noDecisionsForProject')}
        </p>
      )}

      {!isLoading && !isError && recent.length > 0 && (
        <ul aria-label="Recent decisions in this project" className="space-y-1.5">
          {recent.map((d) => (
            <li key={d.id} className="flex gap-2 items-baseline">
              <span
                className="text-caption font-mono shrink-0"
                style={{ color: 'var(--color-text-muted)' }}
              >
                {formatDecisionDate(d.created_at)}
              </span>
              <span className="text-body-sm truncate" style={{ color: 'var(--color-text-primary)' }}>
                {d.title}
              </span>
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}

export function TaskRow({ task, project, expanded, onToggle, onComplete }: TaskRowProps) {
  const { t } = useTranslation()
  const isCompleted = task.status === 'completed'
  const panelId = `task-detail-${task.id}`
  const importance = toImportance(task.priority)

  function handleRowKeyDown(e: React.KeyboardEvent<HTMLDivElement>) {
    if (e.key === 'Enter' || e.key === ' ') {
      e.preventDefault()
      onToggle()
    }
  }

  return (
    <li
      role="group"
      style={{ borderBottom: '1px solid var(--color-border)' }}
    >
      {/* Main row — clickable to toggle */}
      <div
        role="button"
        tabIndex={0}
        aria-expanded={expanded}
        aria-controls={panelId}
        className="flex items-center gap-3 py-3 px-4 transition-colors min-h-[44px] cursor-pointer"
        onClick={onToggle}
        onKeyDown={handleRowKeyDown}
        onMouseEnter={(e) => { e.currentTarget.style.background = 'var(--color-bg-hover)' }}
        onMouseLeave={(e) => { e.currentTarget.style.background = 'transparent' }}
      >
        {/* Checkbox — stopPropagation so it doesn't trigger accordion */}
        <label
          className="shrink-0 cursor-pointer"
          onClick={(e) => e.stopPropagation()}
          onKeyDown={(e) => e.stopPropagation()}
        >
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
                <path
                  d="M1 4L4.5 7.5L11 1"
                  stroke="var(--color-bg-base)"
                  strokeWidth="1.5"
                  strokeLinecap="round"
                  strokeLinejoin="round"
                />
              </svg>
            )}
          </span>
        </label>

        <PriorityDot level={task.priority} />

        <ImportanceBadge importance={importance} dimmed={isCompleted} />

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

          <ChevronDown
            size={14}
            aria-hidden="true"
            className="motion-safe:transition-transform motion-safe:duration-200"
            style={{
              color: 'var(--color-text-muted)',
              transform: expanded ? 'rotate(0deg)' : 'rotate(-90deg)',
            }}
          />
        </div>
      </div>

      {/* Expanded detail panel */}
      {expanded && (
        <section
          id={panelId}
          className="px-4 pb-4 pt-2 grid gap-3 ml-12"
          style={{
            background: 'var(--color-bg-card)',
            borderTop: '1px solid var(--color-border)',
          }}
        >
          {/* Description */}
          <div>
            {task.description ? (
              <p
                className="text-body whitespace-pre-wrap"
                style={{ color: 'var(--color-text-primary)' }}
              >
                {task.description}
              </p>
            ) : (
              <p className="text-body" style={{ color: 'var(--color-text-muted)' }}>
                {t('gtd.noDescription')}
              </p>
            )}
          </div>

          {/* Context line */}
          <div>
            <p className="text-label mb-0.5" style={{ color: 'var(--color-text-muted)' }}>
              {t('gtd.taskContext')}
            </p>
            {task.project_id == null ? (
              <p className="text-body-sm" style={{ color: 'var(--color-text-muted)' }}>
                {t('gtd.unassigned')}
              </p>
            ) : project ? (
              <p className="text-body-sm" style={{ color: 'var(--color-text-primary)' }}>
                {project.title}
                {project.area && (
                  <span style={{ color: 'var(--color-text-muted)' }}> · {project.area}</span>
                )}
              </p>
            ) : (
              <p className="text-body-sm" style={{ color: 'var(--color-warning)' }}>
                {t('gtd.projectNotFound')}
              </p>
            )}
          </div>

          {/* Recent decisions — only when project_id is set */}
          {task.project_id && (
            <RecentDecisions projectId={task.project_id} isExpanded={expanded} />
          )}
        </section>
      )}
    </li>
  )
}
