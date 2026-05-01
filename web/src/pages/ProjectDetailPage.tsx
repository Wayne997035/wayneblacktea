import { useNavigate, useParams } from 'react-router-dom'
import { ArrowLeft } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { useProject } from '../hooks/useProjects'
import { useTasksByProject } from '../hooks/useTasks'
import { useDecisions } from '../hooks/useDecisions'
import { PriorityDot } from '../components/ui/PriorityDot'
import { StatusBadge } from '../components/ui/StatusBadge'
import { LoadingSkeleton } from '../components/ui/LoadingSkeleton'
import { TaskRow } from '../components/gtd/TaskRow'
import { DecisionTimeline } from '../components/decisions/DecisionTimeline'

export function ProjectDetailPage() {
  const { id = '' } = useParams<{ id: string }>()
  const navigate = useNavigate()
  const { t } = useTranslation()

  const projectQuery = useProject(id)
  const tasksQuery = useTasksByProject(id)
  const decisionsQuery = useDecisions(id)

  const project = projectQuery.data
  const tasks = tasksQuery.data ?? []
  const decisions = decisionsQuery.data ?? []

  const pendingTasks = tasks.filter((t) => t.status !== 'done')
  const doneTasks = tasks.filter((t) => t.status === 'done')

  if (projectQuery.isError) {
    return (
      <div className="p-6 max-w-[1200px] mx-auto">
        <button
          type="button"
          onClick={() => navigate(-1)}
          aria-label={t('common.cancel')}
          className="flex items-center gap-2 mb-6 text-body-sm transition-colors"
          style={{ color: 'var(--color-text-muted)', background: 'transparent', border: 'none', cursor: 'pointer', padding: 0 }}
        >
          <ArrowLeft size={16} aria-hidden="true" />
          Back
        </button>
        <div
          className="rounded-md p-3 text-body-sm"
          style={{ background: '#2e0a0a', border: '1px solid var(--color-error)', color: 'var(--color-error)' }}
        >
          {t('error.loadFailed')}
        </div>
      </div>
    )
  }

  return (
    <div className="p-6 max-w-[1200px] mx-auto">
      {/* Back button */}
      <button
        type="button"
        onClick={() => navigate(-1)}
        aria-label="Go back"
        className="flex items-center gap-2 mb-6 text-body-sm transition-colors"
        style={{ color: 'var(--color-text-muted)', background: 'transparent', border: 'none', cursor: 'pointer', padding: 0 }}
        onMouseEnter={(e) => { e.currentTarget.style.color = 'var(--color-text-primary)' }}
        onMouseLeave={(e) => { e.currentTarget.style.color = 'var(--color-text-muted)' }}
      >
        <ArrowLeft size={16} aria-hidden="true" />
        Back
      </button>

      {projectQuery.isLoading || !project ? (
        <div className="space-y-4">
          <LoadingSkeleton className="h-8 w-64" />
          <LoadingSkeleton className="h-4 w-48" />
          <LoadingSkeleton className="h-20 w-full" />
        </div>
      ) : (
        <>
          {/* Header */}
          <div className="mb-6">
            <div className="flex items-start gap-3 mb-2">
              <PriorityDot level={project.priority} />
              <h1 className="text-page-title flex-1" style={{ color: 'var(--color-text-primary)' }}>
                {project.title}
              </h1>
              <StatusBadge status={project.status} size="md" />
            </div>
            <div className="text-body-sm mb-1" style={{ color: 'var(--color-text-muted)' }}>
              {project.area}
              {project.name && (
                <span className="font-mono ml-2" style={{ color: 'var(--color-accent-blue)' }}>
                  {project.name}
                </span>
              )}
            </div>
            {project.description && (
              <p className="text-body mt-2" style={{ color: 'var(--color-text-muted)' }}>
                {project.description}
              </p>
            )}
          </div>

          {/* 2-col on desktop */}
          <div className="grid grid-cols-1 lg:grid-cols-[60%_40%] gap-6">
            {/* Tasks */}
            <section>
              <div className="text-label mb-3" style={{ color: 'var(--color-text-muted)' }}>
                TASKS
                <span
                  className="ml-2 text-caption px-1.5 rounded-full font-mono"
                  style={{ background: 'var(--color-bg-hover)', color: 'var(--color-text-muted)' }}
                >
                  {pendingTasks.length} open
                </span>
              </div>
              {tasksQuery.isLoading ? (
                <div className="space-y-2">
                  {Array.from({ length: 4 }, (_, i) => <LoadingSkeleton key={i} className="h-11 w-full" />)}
                </div>
              ) : tasks.length === 0 ? (
                <p className="text-body-sm" style={{ color: 'var(--color-text-muted)' }}>No tasks yet</p>
              ) : (
                <ul
                  aria-label="Project tasks"
                  className="rounded-lg overflow-hidden"
                  style={{ border: '1px solid var(--color-border)' }}
                >
                  {pendingTasks.map((task) => (
                    <TaskRow key={task.id} task={task} />
                  ))}
                  {doneTasks.map((task) => (
                    <TaskRow key={task.id} task={task} />
                  ))}
                </ul>
              )}
            </section>

            {/* Decisions */}
            <section>
              <div className="text-label mb-3" style={{ color: 'var(--color-text-muted)' }}>
                DECISIONS
              </div>
              {decisionsQuery.isLoading ? (
                <div className="space-y-3">
                  {Array.from({ length: 3 }, (_, i) => <LoadingSkeleton key={i} className="h-16 w-full" />)}
                </div>
              ) : decisions.length === 0 ? (
                <p className="text-body-sm" style={{ color: 'var(--color-text-muted)' }}>No decisions recorded</p>
              ) : (
                <DecisionTimeline decisions={decisions} />
              )}
            </section>
          </div>
        </>
      )}
    </div>
  )
}
