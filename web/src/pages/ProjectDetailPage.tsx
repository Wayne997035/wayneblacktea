import { useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { ArrowLeft, Pencil } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { useProject } from '../hooks/useProjects'
import { useGoals } from '../hooks/useGoals'
import { useTasksByProject, useCompleteTask } from '../hooks/useTasks'
import { useDecisions } from '../hooks/useDecisions'
import { PriorityDot } from '../components/ui/PriorityDot'
import { StatusBadge } from '../components/ui/StatusBadge'
import { LoadingSkeleton } from '../components/ui/LoadingSkeleton'
import { TaskRow } from '../components/gtd/TaskRow'
import { DecisionTimeline } from '../components/decisions/DecisionTimeline'
import { ProjectModal } from '../components/projects/ProjectModal'

// formatCompletedAt renders an ISO timestamp as a short locale-aware
// date string (e.g. "May 7"). Falls back to the raw input on parse failure
// rather than throwing — defensive against backend payloads that drop
// `updated_at` for any reason.
function formatCompletedAt(iso: string | null | undefined): string {
  if (!iso) return ''
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return iso
  return d.toLocaleDateString(undefined, { month: 'short', day: 'numeric' })
}

export function ProjectDetailPage() {
  const { id = '' } = useParams<{ id: string }>()
  const navigate = useNavigate()
  const { t } = useTranslation()
  const [expandedTaskId, setExpandedTaskId] = useState<string | null>(null)
  const [editModalOpen, setEditModalOpen] = useState(false)

  const projectQuery = useProject(id)
  const goalsQuery = useGoals()
  // The project-detail page renders BOTH an "open" and a "completed" section,
  // so it MUST request all statuses. The default (`'active'`) stays in place
  // for GTD list pages — only this page opts in.
  const tasksQuery = useTasksByProject(id, 'all')
  const decisionsQuery = useDecisions(id)
  const completeTask = useCompleteTask(id)

  const project = projectQuery.data
  const tasks = tasksQuery.data ?? []
  const decisions = decisionsQuery.data ?? []
  const goals = goalsQuery.data ?? []

  const pendingTasks = tasks.filter((t) => t.status !== 'completed')
  const doneTasks = tasks.filter((t) => t.status === 'completed')

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
          {t('project.back')}
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
        aria-label={t('project.back')}
        className="flex items-center gap-2 mb-6 text-body-sm transition-colors"
        style={{ color: 'var(--color-text-muted)', background: 'transparent', border: 'none', cursor: 'pointer', padding: 0 }}
        onMouseEnter={(e) => { e.currentTarget.style.color = 'var(--color-text-primary)' }}
        onMouseLeave={(e) => { e.currentTarget.style.color = 'var(--color-text-muted)' }}
      >
        <ArrowLeft size={16} aria-hidden="true" />
        {t('project.back')}
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
              <button
                type="button"
                onClick={() => setEditModalOpen(true)}
                aria-label="Edit project"
                className="flex items-center justify-center w-8 h-8 rounded-md transition-colors flex-shrink-0"
                style={{ color: 'var(--color-text-muted)', background: 'transparent', border: '1px solid var(--color-border)', cursor: 'pointer' }}
                onMouseEnter={(e) => { e.currentTarget.style.background = 'var(--color-bg-hover)' }}
                onMouseLeave={(e) => { e.currentTarget.style.background = 'transparent' }}
              >
                <Pencil size={14} aria-hidden="true" />
              </button>
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
                {t('project.tasksSection')}
                <span
                  className="ml-2 text-caption px-1.5 rounded-full font-mono"
                  style={{ background: 'var(--color-bg-hover)', color: 'var(--color-text-muted)' }}
                >
                  {t('project.openCount', { count: pendingTasks.length })}
                </span>
              </div>
              {tasksQuery.isLoading ? (
                <div className="space-y-2">
                  {Array.from({ length: 4 }, (_, i) => <LoadingSkeleton key={i} className="h-11 w-full" />)}
                </div>
              ) : tasks.length === 0 ? (
                <p className="text-body-sm" style={{ color: 'var(--color-text-muted)' }}>{t('project.noTasks')}</p>
              ) : (
                <>
                  {pendingTasks.length > 0 && (
                    <ul
                      aria-label="Project open tasks"
                      className="rounded-lg overflow-hidden"
                      style={{ border: '1px solid var(--color-border)' }}
                    >
                      {pendingTasks.map((task) => (
                        <TaskRow
                          key={task.id}
                          task={task}
                          project={project}
                          expanded={expandedTaskId === task.id}
                          onToggle={() => setExpandedTaskId((prev) => (prev === task.id ? null : task.id))}
                          onComplete={(taskId) => { void completeTask.mutateAsync(taskId) }}
                        />
                      ))}
                    </ul>
                  )}

                  {doneTasks.length > 0 && (
                    <>
                      <div
                        className="text-label mt-6 mb-3"
                        style={{ color: 'var(--color-text-muted)' }}
                      >
                        {t('project.completedSection')}
                        <span
                          className="ml-2 text-caption px-1.5 rounded-full font-mono"
                          style={{ background: 'var(--color-bg-hover)', color: 'var(--color-text-muted)' }}
                        >
                          {t('project.completedCount', { count: doneTasks.length })}
                        </span>
                      </div>
                      <ul
                        aria-label="Project completed tasks"
                        className="rounded-lg overflow-hidden"
                        style={{ border: '1px solid var(--color-border)' }}
                      >
                        {doneTasks.map((task) => (
                          <TaskRow
                            key={task.id}
                            task={task}
                            project={project}
                            expanded={expandedTaskId === task.id}
                            onToggle={() => setExpandedTaskId((prev) => (prev === task.id ? null : task.id))}
                            onComplete={(taskId) => { void completeTask.mutateAsync(taskId) }}
                            // Completion timestamp comes from updated_at — the
                            // schema sets updated_at = NOW() inside CompleteTask
                            // (sql/queries/gtd.sql); tasks has no completed_at
                            // column. data-completed-at exposes the raw RFC3339
                            // value for tests and a11y tooling.
                            footer={
                              <div
                                data-testid="completed-at"
                                data-completed-at={task.updated_at}
                                className="text-caption px-4 pb-2"
                                style={{ color: 'var(--color-text-muted)' }}
                              >
                                {t('project.completedAt', { date: formatCompletedAt(task.updated_at) })}
                              </div>
                            }
                          />
                        ))}
                      </ul>
                    </>
                  )}
                </>
              )}
            </section>

            {/* Decisions */}
            <section>
              <div className="text-label mb-3" style={{ color: 'var(--color-text-muted)' }}>
                {t('project.decisionsSection')}
              </div>
              {decisionsQuery.isLoading ? (
                <div className="space-y-3">
                  {Array.from({ length: 3 }, (_, i) => <LoadingSkeleton key={i} className="h-16 w-full" />)}
                </div>
              ) : decisions.length === 0 ? (
                <p className="text-body-sm" style={{ color: 'var(--color-text-muted)' }}>{t('project.noDecisions')}</p>
              ) : (
                <DecisionTimeline decisions={decisions} />
              )}
            </section>
          </div>
        </>
      )}

      {editModalOpen && project && (
        <ProjectModal
          entity={project}
          goals={goals}
          onClose={() => setEditModalOpen(false)}
        />
      )}
    </div>
  )
}
