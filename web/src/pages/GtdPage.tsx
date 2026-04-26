import { useState } from 'react'
import { Plus } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { useGoals } from '../hooks/useGoals'
import { useProjects } from '../hooks/useProjects'
import { GoalCard } from '../components/gtd/GoalCard'
import { ProjectList } from '../components/gtd/ProjectList'
import { TaskList } from '../components/gtd/TaskList'
import { QuickAddModal } from '../components/gtd/QuickAddModal'
import { LoadingSkeleton } from '../components/ui/LoadingSkeleton'
import { EmptyState } from '../components/ui/EmptyState'

export function GtdPage() {
  const { t } = useTranslation()
  const [modalOpen, setModalOpen] = useState(false)
  const goalsQuery = useGoals()
  const projectsQuery = useProjects()

  const goals = goalsQuery.data ?? []
  const projects = projectsQuery.data ?? []

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
          <EmptyState messageKey="gtd.noGoals" ctaLabelKey="gtd.addGoal" />
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

      {/* FAB */}
      <button
        type="button"
        onClick={() => setModalOpen(true)}
        aria-label="新增 / Add"
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
        <Plus size={24} aria-hidden="true" style={{ color: 'var(--color-bg-base)' }} />
      </button>

      {modalOpen && (
        <QuickAddModal
          projects={projects}
          onClose={() => setModalOpen(false)}
        />
      )}
    </div>
  )
}
