import { useState } from 'react'
import { Plus, Target, FolderKanban, CheckSquare } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { useGoals } from '../hooks/useGoals'
import { useProjects } from '../hooks/useProjects'
import { GoalCard } from '../components/gtd/GoalCard'
import { ProjectList } from '../components/gtd/ProjectList'
import { TaskList } from '../components/gtd/TaskList'
import { QuickAddModal } from '../components/gtd/QuickAddModal'
import { CreateGoalModal } from '../components/gtd/CreateGoalModal'
import { CreateProjectModal } from '../components/gtd/CreateProjectModal'
import { LoadingSkeleton } from '../components/ui/LoadingSkeleton'
import { EmptyState } from '../components/ui/EmptyState'

type ModalType = 'task' | 'goal' | 'project' | null

export function GtdPage() {
  const { t } = useTranslation()
  const [activeModal, setActiveModal] = useState<ModalType>(null)
  const [fabOpen, setFabOpen] = useState(false)
  const goalsQuery = useGoals()
  const projectsQuery = useProjects()

  const goals = goalsQuery.data ?? []
  const projects = projectsQuery.data ?? []

  const openModal = (type: ModalType) => {
    setFabOpen(false)
    setActiveModal(type)
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
                completedTasks={0}
                totalTasks={0}
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
          <ProjectList projects={projects} />
        )}
      </section>

      {/* Tasks Section */}
      <section>
        <h2 className="text-section mb-4" style={{ color: 'var(--color-text-primary)' }}>
          {t('gtd.tasks')}
        </h2>
        <TaskList projects={projects} />
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
        <CreateGoalModal onClose={() => setActiveModal(null)} />
      )}
      {activeModal === 'project' && (
        <CreateProjectModal
          goals={goals}
          onClose={() => setActiveModal(null)}
        />
      )}
    </div>
  )
}
