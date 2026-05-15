import { useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { useContextToday } from '../hooks/useContextToday'
import { useApiPing } from '../hooks/useApiPing'
import { useDashboardStats } from '../hooks/useDashboardStats'
import { ProjectCard } from '../components/dashboard/ProjectCard'
import { GoalProgress } from '../components/dashboard/GoalProgress'
import { HandoffCard } from '../components/dashboard/HandoffCard'
import { QuickStats } from '../components/dashboard/QuickStats'
import { SystemHealth } from '../components/dashboard/SystemHealth'
import { NextTaskCard } from '../components/dashboard/NextTaskCard'
import { UpcomingTasksCard } from '../components/dashboard/UpcomingTasksCard'
import { RecentDecisionsCard } from '../components/dashboard/RecentDecisionsCard'
import { PendingProposalsCard } from '../components/dashboard/PendingProposalsCard'
import { DueReviewsCard } from '../components/dashboard/DueReviewsCard'
import { RecentKnowledgeCard } from '../components/dashboard/RecentKnowledgeCard'
import { LoadingSkeleton } from '../components/ui/LoadingSkeleton'
import { EmptyState } from '../components/ui/EmptyState'

const UUID_RE = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i

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
  const navigate = useNavigate()
  const { data, isLoading, isError } = useContextToday()
  const pingQuery = useApiPing()
  const statsQuery = useDashboardStats()

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
        {/* Left column: Active Projects + D1 Next Task + D2 Recent Decisions */}
        <div className="flex flex-col gap-6">
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
                    variant="compact"
                    onClick={() => { if (UUID_RE.test(project.id)) navigate(`/workspace/projects/${project.id}`) }}
                  />
                ))}
              </div>
            )}
          </section>

          {/* D1: Next Task */}
          <section>
            <div className="text-label mb-3" style={{ color: 'var(--color-text-muted)' }}>
              {t('dashboard.sections.nextTask')}
            </div>
            <NextTaskCard onNavigate={(id) => { if (UUID_RE.test(id)) navigate(`/gtd?task_id=${id}`) }} />
          </section>

          {/* D1b: Upcoming Tasks */}
          <section>
            <div className="text-label mb-3" style={{ color: 'var(--color-text-muted)' }}>
              {t('dashboard.sections.upcomingTasks', 'Upcoming Tasks')}
            </div>
            <UpcomingTasksCard />
          </section>

          {/* D2: Recent Decisions */}
          <section>
            <RecentDecisionsCard onSeeAll={() => navigate('/decisions')} />
          </section>
        </div>

        {/* Right column: Progress + Handoff + Stats + Health */}
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

          {/* D3: Pending Proposals */}
          <section>
            <PendingProposalsCard onClick={() => navigate('/proposals')} />
          </section>

          {/* D4: Due Reviews */}
          <section>
            <DueReviewsCard onClick={() => navigate('/reviews')} />
          </section>

          {/* D5: Recent Knowledge */}
          <section>
            <RecentKnowledgeCard onClick={() => navigate('/knowledge')} />
          </section>

          {/* Quick Stats — now uses real API data */}
          <section>
            <QuickStats
              pendingTasks={statsQuery.isLoading ? null : (statsQuery.data?.task_total ?? null)}
              decisionsToday={statsQuery.isLoading ? null : (statsQuery.data?.decision_count ?? null)}
              onPendingTasksClick={() => navigate('/gtd')}
            />
          </section>

          {/* System Health */}
          <section>
            <SystemHealth isOnline={!pingQuery.isError} isLoading={pingQuery.isLoading} />
          </section>
        </div>
      </div>
    </div>
  )
}
