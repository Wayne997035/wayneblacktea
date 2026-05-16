import { useId } from 'react'
import { Clock } from 'lucide-react'
import { Link } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { LoadingSkeleton } from '../ui/LoadingSkeleton'
import { EmptyState } from '../ui/EmptyState'
import { useDashboardUpcoming, type UpcomingTask } from '../../hooks/useDashboardUpcoming'

const MAX_TASKS = 5

function formatDueDate(due: string): string {
  const date = new Date(due)
  const now = new Date()

  const todayStart = new Date(now.getFullYear(), now.getMonth(), now.getDate())
  const dateStart = new Date(date.getFullYear(), date.getMonth(), date.getDate())

  const diff = dateStart.getTime() - todayStart.getTime()

  if (diff === 0) return 'Today'
  if (diff === 86_400_000) return 'Tomorrow'
  return date.toLocaleDateString('en', { month: 'short', day: 'numeric' })
}

interface TaskRowProps {
  task: UpcomingTask
}

function TaskRow({ task }: TaskRowProps) {
  return (
    <Link
      to={`/gtd?task_id=${task.id}`}
      className="flex items-center justify-between gap-2 py-1.5 px-2 rounded"
      style={{
        color: 'var(--color-text-primary)',
        textDecoration: 'none',
      }}
      onMouseEnter={(e) => {
        e.currentTarget.style.background = 'var(--color-bg-hover)'
      }}
      onMouseLeave={(e) => {
        e.currentTarget.style.background = 'transparent'
      }}
    >
      <span className="text-body-sm flex-1 min-w-0 truncate">{task.title}</span>
      <span
        className="text-caption shrink-0"
        style={{ color: 'var(--color-text-muted)' }}
      >
        {formatDueDate(task.due_date)}
      </span>
    </Link>
  )
}

export function UpcomingNearTermCard() {
  const headerId = useId()
  const { t } = useTranslation()
  const { data, isLoading } = useDashboardUpcoming()

  if (isLoading) {
    return (
      <div
        className="rounded-lg p-4"
        style={{
          background: 'var(--color-bg-card)',
          border: '1px solid var(--color-border)',
          minHeight: '120px',
        }}
      >
        <div className="flex items-center gap-2 mb-3">
          <LoadingSkeleton style={{ width: '16px', height: '16px' }} />
          <LoadingSkeleton style={{ width: '120px', height: '14px' }} />
        </div>
        <div className="flex flex-col gap-2">
          {Array.from({ length: 3 }, (_, i) => (
            <LoadingSkeleton key={i} style={{ width: '100%', height: '14px' }} />
          ))}
        </div>
      </div>
    )
  }

  const tasks = (data ?? []).slice(0, MAX_TASKS)

  return (
    <section
      aria-labelledby={headerId}
      className="rounded-lg p-4"
      style={{
        background: 'var(--color-bg-card)',
        border: '1px solid var(--color-border)',
      }}
    >
      <div className="flex items-center gap-2 mb-3">
        <Clock size={16} aria-hidden="true" style={{ color: 'var(--color-accent-blue)' }} />
        <span
          id={headerId}
          className="text-label"
          style={{ color: 'var(--color-text-muted)' }}
        >
          {t('dashboard.widgets.nearTermTasks', 'DUE THIS WEEK')}
        </span>
      </div>

      {tasks.length === 0 ? (
        <EmptyState messageKey="dashboard.noNearTermTasks" />
      ) : (
        <div role="list">
          {tasks.map((task) => (
            <div key={task.id} role="listitem">
              <TaskRow task={task} />
            </div>
          ))}
        </div>
      )}
    </section>
  )
}
