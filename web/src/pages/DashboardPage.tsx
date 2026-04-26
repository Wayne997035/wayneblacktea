import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useContextToday } from '../hooks/useContextToday'
import { ProjectCard } from '../components/dashboard/ProjectCard'
import { GoalProgress } from '../components/dashboard/GoalProgress'
import { HandoffCard } from '../components/dashboard/HandoffCard'
import { QuickStats } from '../components/dashboard/QuickStats'
import { LoadingSkeleton } from '../components/ui/LoadingSkeleton'
import { EmptyState } from '../components/ui/EmptyState'

function getGreetingKey(): string {
  const hour = new Date().getHours()
  if (hour < 12) return 'dashboard.greeting.morning'
  if (hour < 18) return 'dashboard.greeting.afternoon'
  return 'dashboard.greeting.evening'
}

function formatDate(date: Date): string {
  return date.toLocaleDateString(undefined, {
    weekday: 'long',
    day: 'numeric',
    month: 'long',
    year: 'numeric',
  })
}

export function DashboardPage() {
  const { t } = useTranslation()
  const { data, isLoading, isError } = useContextToday()
  const [expandedId, setExpandedId] = useState<string | null>(null)

  const activeProjects = (data?.projects ?? [])
    .filter((p) => p.status === 'active')
    .sort((a, b) => b.priority - a.priority)

  const weeklyProgress = data?.weekly_progress ?? { completed: 0, total: 0 }

  return (
    <div className="p-6 max-w-[1200px] mx-auto">
      {/* Greeting row */}
      <div className="flex items-center justify-between py-3 mb-6">
        <h1 className="text-section" style={{ color: 'var(--color-text-primary)' }}>
          {t(getGreetingKey())}
        </h1>
        <span className="text-body" style={{ color: 'var(--color-text-muted)' }}>
          {formatDate(new Date())}
        </span>
      </div>

      {isError && (
        <div
          className="rounded-md p-3 mb-6 text-body-sm flex items-center justify-between"
          style={{
            background: '#2e0a0a',
            border: '1px solid var(--color-error)',
            color: 'var(--color-error)',
          }}
        >
          <span>{t('error.loadFailed')}</span>
        </div>
      )}

      {/* 2-col layout on desktop/tablet */}
      <div className="grid grid-cols-1 lg:grid-cols-[60%_40%] gap-6">
        {/* Left: Active Projects */}
        <section>
          <div className="text-label mb-3" style={{ color: 'var(--color-text-muted)' }}>
            {t('dashboard.sections.activeProjects')}
          </div>
          {isLoading ? (
            <div className="flex flex-col gap-3">
              {Array.from({ length: 3 }, (_, i) => (
                <LoadingSkeleton key={i} className="w-full" style={{ height: '96px' }} />
              ))}
            </div>
          ) : activeProjects.length === 0 ? (
            <EmptyState messageKey="dashboard.noProjects" />
          ) : (
            <div className="flex flex-col gap-3">
              {activeProjects.map((project) => (
                <ProjectCard
                  key={project.id}
                  project={project}
                  variant={expandedId === project.id ? 'expanded' : 'compact'}
                  onClick={() => setExpandedId(expandedId === project.id ? null : project.id)}
                />
              ))}
            </div>
          )}
        </section>

        {/* Right: Progress + Handoff + Stats */}
        <div className="flex flex-col gap-6">
          {/* Weekly Progress */}
          <section>
            <div className="text-label mb-3" style={{ color: 'var(--color-text-muted)' }}>
              {t('dashboard.sections.weeklyProgress')}
            </div>
            {isLoading ? (
              <LoadingSkeleton className="w-[80px] h-[80px] mx-auto rounded-full" />
            ) : weeklyProgress.total === 0 ? (
              <EmptyState messageKey="dashboard.noTasksThisWeek" />
            ) : (
              <div className="flex justify-center">
                <GoalProgress
                  completed={weeklyProgress.completed}
                  total={weeklyProgress.total}
                />
              </div>
            )}
          </section>

          {/* Handoff */}
          <section>
            <div className="text-label mb-3" style={{ color: 'var(--color-text-muted)' }}>
              {t('dashboard.sections.nextSession')}
            </div>
            {isLoading ? (
              <LoadingSkeleton className="w-full" style={{ height: '120px' }} />
            ) : (
              <HandoffCard handoff={data?.pending_handoff ?? null} />
            )}
          </section>

          {/* Quick Stats */}
          <section>
            <QuickStats
              pendingTasks={isLoading ? null : ((data?.projects ?? []).length)}
              decisionsToday={null}
            />
          </section>
        </div>
      </div>
    </div>
  )
}
