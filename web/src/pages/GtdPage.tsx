import { useState, useMemo } from 'react'
import { Plus, Target, FolderKanban, CheckSquare } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { useSearchParams } from 'react-router-dom'
import { useGoals } from '../hooks/useGoals'
import { useProjects } from '../hooks/useProjects'
import { useAllTasks } from '../hooks/useTasks'
import { GoalCard } from '../components/gtd/GoalCard'
import { ProjectList } from '../components/gtd/ProjectList'
import { TaskList } from '../components/gtd/TaskList'
import { QuickAddModal } from '../components/gtd/QuickAddModal'
import { GoalModal } from '../components/goals/GoalModal'
import { ProjectModal } from '../components/projects/ProjectModal'
import { LoadingSkeleton } from '../components/ui/LoadingSkeleton'
import { EmptyState } from '../components/ui/EmptyState'
import type { Goal, Project } from '../types/api'

type ModalType = 'task' | 'goal' | 'project' | 'editGoal' | 'editProject' | null

export function GtdPage() {
  const { t } = useTranslation()
  const [searchParams] = useSearchParams()
  const linkedTaskId = searchParams.get('task_id')
  const [activeModal, setActiveModal] = useState<ModalType>(null)
  const [editGoal, setEditGoal] = useState<Goal | null>(null)
  const [editProject, setEditProject] = useState<Project | null>(null)
  const [fabOpen, setFabOpen] = useState(false)
  const goalsQuery = useGoals()
  // 'all' so goalTaskCounts below can map every task back to its project's
  // goal — a completed/archived project excluded here would silently drop
  // its tasks out of the per-goal progress count (see GoalCard).
  const projectsQuery = useProjects('all')
  const tasksQuery = useAllTasks('all')

  const goals = goalsQuery.data ?? []
  const projects = projectsQuery.data ?? []

  // Build per-goal task counts: task -> project -> goal
  // Using projectsQuery.data / tasksQuery.data (stable TanStack Query refs) as deps
  const goalTaskCounts = useMemo(() => {
    const projectData = projectsQuery.data
    const taskData = tasksQuery.data
    if (!projectData || !taskData) return {}

    const projectIdToGoalId = new Map<string, string>()
    for (const project of projectData) {
      if (project.goal_id) {
        projectIdToGoalId.set(project.id, project.goal_id)
      }
    }
    const counts: Record<string, { completed: number; total: number }> = {}
    for (const task of taskData) {
      if (!task.project_id) continue
      const goalId = projectIdToGoalId.get(task.project_id)
      if (!goalId) continue
      if (!counts[goalId]) counts[goalId] = { completed: 0, total: 0 }
      counts[goalId].total++
      if (task.status === 'completed') counts[goalId].completed++
    }
    return counts
  }, [projectsQuery.data, tasksQuery.data])

  const openModal = (type: ModalType) => {
    setFabOpen(false)
    setActiveModal(type)
  }

  function handleModalClose() {
    setActiveModal(null)
    setEditGoal(null)
    setEditProject(null)
  }

  return (
    <div className="p-6 max-w-[1200px] mx-auto pb-24">
      <div className="flex items-center justify-between mb-6">
        <h1 className="text-page-title" style={{ color: 'var(--color-text-primary)' }}>
          {t('gtd.title')}
        </h1>
      </div>

      {/* Goals Section */}
      <section className="mb-8">
        <h2 className="text-section mb-4" style={{ color: 'var(--color-text-primary)' }}>
          {t('gtd.goals')}
        </h2>
        {goalsQuery.isLoading ? (
          <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-4">
            {Array.from({ length: 3 }, (_, i) => (
              <LoadingSkeleton key={i} className="h-40 w-full" />
            ))}
          </div>
        ) : goals.length === 0 ? (
          <EmptyState messageKey="gtd.noGoals" ctaLabelKey="gtd.addGoal" onCta={() => openModal('goal')} />
        ) : (
          <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-4">
            {goals.map((goal) => (
              <GoalCard
                key={goal.id}
                goal={goal}
                completedTasks={goalTaskCounts[goal.id]?.completed ?? 0}
                totalTasks={goalTaskCounts[goal.id]?.total ?? 0}
                onEdit={(g) => { setEditGoal(g); setActiveModal('editGoal') }}
              />
            ))}
          </div>
        )}
      </section>

      {/* Projects Section */}
      <section className="mb-8">
        <h2 className="text-section mb-4" style={{ color: 'var(--color-text-primary)' }}>
          {t('gtd.projects')}
        </h2>
        {projectsQuery.isLoading ? (
          <div className="flex flex-col gap-3">
            {Array.from({ length: 3 }, (_, i) => (
              <LoadingSkeleton key={i} className="h-24 w-full" />
            ))}
          </div>
        ) : (
          <ProjectList
            projects={projects}
            onEdit={(p) => { setEditProject(p); setActiveModal('editProject') }}
          />
        )}
      </section>

      {/* Tasks Section */}
      <section>
        <h2 className="text-section mb-4" style={{ color: 'var(--color-text-primary)' }}>
          {t('gtd.tasks')}
        </h2>
        <TaskList projects={projects} linkedTaskId={linkedTaskId ?? undefined} />
      </section>

      {/* FAB backdrop */}
      {fabOpen && (
        <div
          className="fixed inset-0 z-40"
          aria-hidden="true"
          onClick={() => setFabOpen(false)}
        />
      )}

      {/* FAB sub-menu */}
      {fabOpen && (
        <div
          className="fixed bottom-24 right-6 z-50 flex flex-col gap-2"
          role="menu"
          aria-label="Add options"
        >
          {(
            [
              { icon: <Target size={16} aria-hidden="true" />, labelKey: 'gtd.addGoal', type: 'goal' as ModalType },
              { icon: <FolderKanban size={16} aria-hidden="true" />, labelKey: 'gtd.addProject', type: 'project' as ModalType },
              { icon: <CheckSquare size={16} aria-hidden="true" />, labelKey: 'gtd.addTask', type: 'task' as ModalType },
            ] as const
          ).map(({ icon, labelKey, type }) => (
            <button
              key={type}
              type="button"
              role="menuitem"
              onClick={() => openModal(type)}
              className="flex items-center gap-2 px-4 py-2 rounded-full text-body-sm font-medium transition-all"
              style={{
                background: 'var(--color-bg-card)',
                border: '1px solid var(--color-accent-blue)',
                color: 'var(--color-text-primary)',
                cursor: 'pointer',
                whiteSpace: 'nowrap',
              }}
              onMouseEnter={(e) => {
                e.currentTarget.style.background = 'var(--color-bg-hover)'
              }}
              onMouseLeave={(e) => {
                e.currentTarget.style.background = 'var(--color-bg-card)'
              }}
            >
              {icon}
              {t(labelKey)}
            </button>
          ))}
        </div>
      )}

      <button
        type="button"
        onClick={() => setFabOpen((v) => !v)}
        aria-label="Add"
        aria-expanded={fabOpen}
        className="fixed bottom-6 right-6 z-50 w-14 h-14 rounded-full flex items-center justify-center transition-all"
        style={{
          background: 'var(--color-accent-blue)',
          border: 'none',
          cursor: 'pointer',
          boxShadow: '0 4px 16px rgba(79, 195, 247, 0.3)',
        }}
        onMouseEnter={(e) => {
          e.currentTarget.style.background = '#81d4fa'
          e.currentTarget.style.transform = 'scale(1.05)'
        }}
        onMouseLeave={(e) => {
          e.currentTarget.style.background = 'var(--color-accent-blue)'
          e.currentTarget.style.transform = 'scale(1)'
        }}
      >
        <Plus
          size={24}
          aria-hidden="true"
          style={{
            color: 'var(--color-bg-base)',
            transition: 'transform 200ms',
            transform: fabOpen ? 'rotate(45deg)' : 'rotate(0deg)',
          }}
        />
      </button>

      {activeModal === 'task' && (
        <QuickAddModal
          projects={projects}
          onClose={() => setActiveModal(null)}
        />
      )}
      {activeModal === 'goal' && (
        <GoalModal onClose={() => setActiveModal(null)} />
      )}
      {activeModal === 'project' && (
        <ProjectModal
          goals={goals}
          onClose={() => setActiveModal(null)}
        />
      )}
      {activeModal === 'editGoal' && editGoal && (
        <GoalModal entity={editGoal} onClose={handleModalClose} />
      )}
      {activeModal === 'editProject' && editProject && (
        <ProjectModal entity={editProject} goals={goals} onClose={handleModalClose} />
      )}
    </div>
  )
}
