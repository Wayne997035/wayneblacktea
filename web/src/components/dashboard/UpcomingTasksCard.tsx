import { Calendar } from 'lucide-react'
import { Link } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { LoadingSkeleton } from '../ui/LoadingSkeleton'
import { EmptyState } from '../ui/EmptyState'
import {
  useDashboardUpcomingTasks,
  type UpcomingTaskItem,
} from '../../hooks/useDashboardUpcomingTasks'

interface UpcomingTasksCardProps {
  days?: number
}

interface BucketSection {
  key: string
  label: string
  tasks: UpcomingTaskItem[]
}

function formatDueDate(due: string): string {
  return new Date(due).toLocaleDateString('en', { month: 'short', day: 'numeric' })
}

function TaskRow({ task }: { task: UpcomingTaskItem }) {
  return (
    <Link
      to={`/gtd?task_id=${task.id}`}
      className="flex items-start justify-between gap-2 py-1.5 px-2 rounded"
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
      <span className="text-caption shrink-0" style={{ color: 'var(--color-text-muted)' }}>
        {task.due_date ? formatDueDate(task.due_date) : '—'}
      </span>
    </Link>
  )
}

function BucketBlock({ label, tasks }: { label: string; tasks: UpcomingTaskItem[] }) {
  if (tasks.length === 0) return null
  return (
    <div className="mb-3">
      <div
        className="text-caption font-medium mb-1 px-2"
        style={{ color: 'var(--color-text-muted)', textTransform: 'uppercase', letterSpacing: '0.04em' }}
      >
        {label}
      </div>
      <div>
        {tasks.map((t) => (
          <TaskRow key={t.id} task={t} />
        ))}
      </div>
    </div>
  )
}

export function UpcomingTasksCard({ days = 7 }: UpcomingTasksCardProps) {
  const { t } = useTranslation()
  const { data, isLoading } = useDashboardUpcomingTasks(days)

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
        <LoadingSkeleton style={{ width: '120px', height: '14px' }} />
        <div className="mt-3 flex flex-col gap-2">
          {Array.from({ length: 4 }, (_, i) => (
            <LoadingSkeleton key={i} style={{ width: '100%', height: '14px' }} />
          ))}
        </div>
      </div>
    )
  }

  const groups = data?.groups

  const buckets: BucketSection[] = [
    { key: 'today', label: t('dashboard.upcoming.today', 'Today'), tasks: groups?.today ?? [] },
    { key: 'tomorrow', label: t('dashboard.upcoming.tomorrow', 'Tomorrow'), tasks: groups?.tomorrow ?? [] },
    { key: 'day_after', label: t('dashboard.upcoming.dayAfter', 'Day After'), tasks: groups?.day_after ?? [] },
    { key: 'upcoming', label: t('dashboard.upcoming.upcoming', 'Upcoming'), tasks: groups?.upcoming ?? [] },
    {
      key: 'unscheduled_important',
      label: t('dashboard.upcoming.unscheduledImportant', 'Active / No Due Date'),
      tasks: groups?.unscheduled_important ?? [],
    },
  ]

  const hasAnyTask = buckets.some((b) => b.tasks.length > 0)

  return (
    <div
      className="rounded-lg p-4"
      style={{
        background: 'var(--color-bg-card)',
        border: '1px solid var(--color-border)',
      }}
    >
      <div className="flex items-center gap-2 mb-3">
        <Calendar size={16} aria-hidden="true" style={{ color: 'var(--color-accent-blue)' }} />
        <span className="text-label" style={{ color: 'var(--color-text-muted)' }}>
          {t('dashboard.widgets.upcomingTasks', 'Upcoming Tasks')}
        </span>
      </div>

      {!hasAnyTask ? (
        <EmptyState messageKey="dashboard.noUpcomingTasks" />
      ) : (
        <div>
          {buckets.map((b) => (
            <BucketBlock key={b.key} label={b.label} tasks={b.tasks} />
          ))}
        </div>
      )}
    </div>
  )
}
