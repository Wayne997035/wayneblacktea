import { useNavigate, useParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { ArrowLeft, GitBranch } from 'lucide-react'
import { useRepoOverview } from '../hooks/useRepoOverview'
import { LoadingSkeleton } from '../components/ui/LoadingSkeleton'
import { kindColor } from '../components/calendar/eventStyles'
import type {
  RepoOverviewActivity,
  RepoOverviewCompletedTask,
  RepoOverviewDecision,
  RepoOverviewHandoff,
  RepoOverviewPendingTask,
  RepoOverviewSummary,
} from '../types/api'

const languageBadgeColors: Record<string, { bg: string; color: string }> = {
  Go: { bg: '#00ADD8', color: '#fff' },
  TypeScript: { bg: '#3178C6', color: '#fff' },
  Java: { bg: '#B07219', color: '#fff' },
}

function fmtDate(iso?: string): string {
  if (!iso) return ''
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return ''
  return d.toLocaleDateString(undefined, { year: 'numeric', month: 'short', day: 'numeric' })
}

function fmtMonth(iso?: string): string {
  if (!iso) return ''
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return ''
  return d.toLocaleDateString(undefined, { year: 'numeric', month: 'long' })
}

/**
 * groupByMonth buckets completed tasks by their `completed_at` month label.
 * Order is preserved from the input array (newest first), so the resulting
 * map iterates newest-month-first when iterated via Object.entries.
 */
function groupByMonth(tasks: RepoOverviewCompletedTask[]): Map<string, RepoOverviewCompletedTask[]> {
  const groups = new Map<string, RepoOverviewCompletedTask[]>()
  for (const task of tasks) {
    const label = fmtMonth(task.completed_at) || 'Earlier'
    const existing = groups.get(label) ?? []
    existing.push(task)
    groups.set(label, existing)
  }
  return groups
}

function LanguageBadge({ language }: { language: string | undefined }) {
  if (!language) return null
  const style = languageBadgeColors[language] ?? { bg: 'var(--color-bg-hover)', color: 'var(--color-text-muted)' }
  return (
    <span
      className="text-label px-2 py-0.5 rounded-full"
      style={{ background: style.bg, color: style.color }}
    >
      {language}
    </span>
  )
}

function HeaderSection({ repo }: { repo: RepoOverviewSummary }) {
  const { t } = useTranslation()
  return (
    <header className="mb-6">
      <div className="flex items-center gap-3 mb-2">
        <LanguageBadge language={repo.language} />
        <h1
          className="font-mono text-page-title flex-1"
          style={{ color: 'var(--color-text-primary)' }}
        >
          {repo.name}
        </h1>
        <span
          className="w-2 h-2 rounded-full shrink-0"
          style={{ background: repo.status === 'active' ? 'var(--color-success)' : 'var(--color-text-muted)' }}
          aria-label={`Status: ${repo.status}`}
        />
      </div>
      {repo.description && (
        <p className="text-body-sm mb-2" style={{ color: 'var(--color-text-muted)' }}>
          {repo.description}
        </p>
      )}
      <div className="flex flex-wrap gap-4 text-caption" style={{ color: 'var(--color-text-muted)' }}>
        {repo.current_branch && (
          <span className="flex items-center gap-1 font-mono">
            <GitBranch size={12} aria-hidden="true" />
            {t('workspace.repoDetail.branch')}: {repo.current_branch}
          </span>
        )}
        {repo.last_activity && (
          <span>
            {t('workspace.repoDetail.lastActivity')}: {fmtDate(repo.last_activity)}
          </span>
        )}
      </div>
      {repo.next_planned_step && (
        <p className="text-body-sm mt-2" style={{ color: 'var(--color-text-muted)' }}>
          <span style={{ color: 'var(--color-accent-blue)' }}>
            {t('workspace.repoDetail.nextStep')}:
          </span>{' '}
          {repo.next_planned_step}
        </p>
      )}
    </header>
  )
}

function PendingSection({ tasks }: { tasks: RepoOverviewPendingTask[] }) {
  const { t } = useTranslation()
  // Sort: importance ASC NULLS LAST, due_date ASC NULLS LAST, priority ASC.
  const sorted = [...tasks].sort((a, b) => {
    const ai = a.importance ?? 99
    const bi = b.importance ?? 99
    if (ai !== bi) return ai - bi
    const ad = a.due_date ? Date.parse(a.due_date) : Infinity
    const bd = b.due_date ? Date.parse(b.due_date) : Infinity
    if (ad !== bd) return ad - bd
    return a.priority - b.priority
  })
  return (
    <section aria-labelledby="repo-detail-pending">
      <h2
        id="repo-detail-pending"
        className="text-label mb-3"
        style={{ color: 'var(--color-text-muted)' }}
      >
        {t('workspace.repoDetail.pending')}
        <span
          className="ml-2 text-caption px-1.5 rounded-full font-mono"
          style={{ background: 'var(--color-bg-hover)', color: 'var(--color-text-muted)' }}
        >
          {t('workspace.repoDetail.pendingCount', { count: sorted.length })}
        </span>
      </h2>
      {sorted.length === 0 ? (
        <p className="text-body-sm" style={{ color: 'var(--color-text-muted)' }}>
          {t('workspace.repoDetail.noPending')}
        </p>
      ) : (
        <ul
          className="rounded-lg overflow-hidden"
          style={{ border: '1px solid var(--color-border)' }}
        >
          {sorted.map((task) => (
            <li
              key={task.id}
              className="flex items-center gap-3 px-3 py-2"
              style={{ borderBottom: '1px solid var(--color-border)', background: 'var(--color-bg-card)' }}
            >
              <span className="font-mono text-caption shrink-0" style={{ color: 'var(--color-text-muted)' }}>
                P{task.priority}
              </span>
              <span className="flex-1 text-body-sm" style={{ color: 'var(--color-text-primary)' }}>
                {task.title}
              </span>
              {task.due_date && (
                <span className="text-caption shrink-0" style={{ color: 'var(--color-text-muted)' }}>
                  {fmtDate(task.due_date)}
                </span>
              )}
            </li>
          ))}
        </ul>
      )}
    </section>
  )
}

function CompletedSection({ tasks }: { tasks: RepoOverviewCompletedTask[] }) {
  const { t } = useTranslation()
  const groups = groupByMonth(tasks)
  return (
    <section aria-labelledby="repo-detail-completed">
      <h2
        id="repo-detail-completed"
        className="text-label mb-3"
        style={{ color: 'var(--color-text-muted)' }}
      >
        {t('workspace.repoDetail.completed')}
        <span
          className="ml-2 text-caption px-1.5 rounded-full font-mono"
          style={{ background: 'var(--color-bg-hover)', color: 'var(--color-text-muted)' }}
        >
          {t('workspace.repoDetail.completedCount', { count: tasks.length })}
        </span>
      </h2>
      {tasks.length === 0 ? (
        <p className="text-body-sm" style={{ color: 'var(--color-text-muted)' }}>
          {t('workspace.repoDetail.noCompleted')}
        </p>
      ) : (
        <div className="space-y-3">
          {Array.from(groups.entries()).map(([label, items], idx) => (
            <details
              key={label}
              open={idx === 0}
              className="rounded-lg"
              style={{ border: '1px solid var(--color-border)', background: 'var(--color-bg-card)' }}
            >
              <summary
                className="cursor-pointer px-3 py-2 text-body-sm font-medium"
                style={{ color: 'var(--color-text-primary)' }}
              >
                {label}
                <span
                  className="ml-2 text-caption font-mono"
                  style={{ color: 'var(--color-text-muted)' }}
                >
                  ({items.length})
                </span>
              </summary>
              <ul>
                {items.map((task) => (
                  <li
                    key={task.id}
                    className="px-3 py-2 text-body-sm flex items-center gap-3"
                    style={{ borderTop: '1px solid var(--color-border)', color: 'var(--color-text-primary)' }}
                  >
                    <span className="flex-1">{task.title}</span>
                    {task.artifact && (
                      <a
                        href={task.artifact}
                        target="_blank"
                        rel="noopener noreferrer"
                        className="text-caption font-mono"
                        style={{ color: 'var(--color-accent-blue)' }}
                      >
                        ↗
                      </a>
                    )}
                    <span className="text-caption shrink-0" style={{ color: 'var(--color-text-muted)' }}>
                      {fmtDate(task.completed_at)}
                    </span>
                  </li>
                ))}
              </ul>
            </details>
          ))}
        </div>
      )}
    </section>
  )
}

function DecisionsSection({ decisions }: { decisions: RepoOverviewDecision[] }) {
  const { t } = useTranslation()
  return (
    <section aria-labelledby="repo-detail-decisions">
      <h2
        id="repo-detail-decisions"
        className="text-label mb-3"
        style={{ color: 'var(--color-text-muted)' }}
      >
        {t('workspace.repoDetail.decisions')}
      </h2>
      {decisions.length === 0 ? (
        <p className="text-body-sm" style={{ color: 'var(--color-text-muted)' }}>
          {t('workspace.repoDetail.noDecisions')}
        </p>
      ) : (
        <ul className="space-y-3">
          {decisions.map((d) => (
            <li
              key={d.id}
              className="rounded-lg p-3"
              style={{ background: 'var(--color-bg-card)', border: '1px solid var(--color-border)' }}
            >
              <div className="flex items-baseline gap-2 mb-1">
                <span className="font-medium text-body-sm" style={{ color: 'var(--color-text-primary)' }}>
                  {d.title}
                </span>
                <span className="text-caption ml-auto" style={{ color: 'var(--color-text-muted)' }}>
                  {fmtDate(d.created_at)}
                </span>
              </div>
              <p className="text-body-sm mb-1" style={{ color: 'var(--color-text-primary)' }}>
                {d.decision}
              </p>
              {d.rationale && (
                <p className="text-caption" style={{ color: 'var(--color-text-muted)' }}>
                  <span className="uppercase mr-1">{t('workspace.repoDetail.rationaleLabel')}:</span>
                  {d.rationale}
                </p>
              )}
            </li>
          ))}
        </ul>
      )}
    </section>
  )
}

function ActivitySection({ activity }: { activity: RepoOverviewActivity[] }) {
  const { t } = useTranslation()
  return (
    <section aria-labelledby="repo-detail-activity">
      <h2
        id="repo-detail-activity"
        className="text-label mb-3"
        style={{ color: 'var(--color-text-muted)' }}
      >
        {t('workspace.repoDetail.activity')}
      </h2>
      {activity.length === 0 ? (
        <p className="text-body-sm" style={{ color: 'var(--color-text-muted)' }}>
          {t('workspace.repoDetail.noActivity')}
        </p>
      ) : (
        <ul
          className="rounded-lg overflow-hidden"
          style={{ border: '1px solid var(--color-border)' }}
        >
          {activity.map((a) => {
            const colour = kindColor(a.kind)
            return (
              <li
                key={a.id}
                className="px-3 py-2 flex items-center gap-3 text-body-sm"
                style={{
                  borderBottom: '1px solid var(--color-border)',
                  background: 'var(--color-bg-card)',
                }}
              >
                <span
                  className={`w-2 h-2 rounded-full shrink-0 ${colour.dot}`}
                  aria-label={a.kind}
                />
                <span className="flex-1" style={{ color: 'var(--color-text-primary)' }}>
                  {a.notes || a.action}
                </span>
                <span className="text-caption shrink-0" style={{ color: 'var(--color-text-muted)' }}>
                  {fmtDate(a.created_at)}
                </span>
              </li>
            )
          })}
        </ul>
      )}
    </section>
  )
}

function HandoffsSection({ handoffs }: { handoffs: RepoOverviewHandoff[] }) {
  const { t } = useTranslation()
  return (
    <section aria-labelledby="repo-detail-handoffs">
      <h2
        id="repo-detail-handoffs"
        className="text-label mb-3"
        style={{ color: 'var(--color-text-muted)' }}
      >
        {t('workspace.repoDetail.handoffs')}
      </h2>
      {handoffs.length === 0 ? (
        <p className="text-body-sm" style={{ color: 'var(--color-text-muted)' }}>
          {t('workspace.repoDetail.noHandoffs')}
        </p>
      ) : (
        <ul className="space-y-2">
          {handoffs.map((h) => (
            <li
              key={h.id}
              className="rounded-lg p-3 flex items-start gap-3"
              style={{ background: 'var(--color-bg-card)', border: '1px solid var(--color-border)' }}
            >
              <span
                className="text-caption px-1.5 rounded-full font-mono shrink-0 mt-0.5"
                style={{
                  background: h.status === 'open' ? 'var(--color-warning)' : 'var(--color-success)',
                  color: '#fff',
                }}
              >
                {h.status === 'open'
                  ? t('workspace.repoDetail.openHandoff')
                  : t('workspace.repoDetail.resolvedHandoff')}
              </span>
              <span className="flex-1 text-body-sm" style={{ color: 'var(--color-text-primary)' }}>
                {h.intent}
              </span>
              <span className="text-caption shrink-0" style={{ color: 'var(--color-text-muted)' }}>
                {fmtDate(h.created_at)}
              </span>
            </li>
          ))}
        </ul>
      )}
    </section>
  )
}

export function RepoDetailPage() {
  const { id = '' } = useParams<{ id: string }>()
  const navigate = useNavigate()
  const { t } = useTranslation()
  const overviewQuery = useRepoOverview(id)

  if (overviewQuery.isError) {
    return (
      <div className="p-6 max-w-[1200px] mx-auto">
        <BackButton onClick={() => navigate('/workspace')} label={t('workspace.repoDetail.back')} />
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
      <BackButton onClick={() => navigate('/workspace')} label={t('workspace.repoDetail.back')} />
      {overviewQuery.isLoading || !overviewQuery.data ? (
        <div className="space-y-4">
          <LoadingSkeleton className="h-8 w-64" />
          <LoadingSkeleton className="h-4 w-48" />
          <LoadingSkeleton className="h-20 w-full" />
        </div>
      ) : (
        <>
          <HeaderSection repo={overviewQuery.data.repo} />
          <div className="grid grid-cols-1 lg:grid-cols-2 gap-6">
            <div className="space-y-6">
              <PendingSection tasks={overviewQuery.data.pending_tasks} />
              <CompletedSection tasks={overviewQuery.data.completed_tasks} />
            </div>
            <div className="space-y-6">
              <DecisionsSection decisions={overviewQuery.data.recent_decisions} />
              <ActivitySection activity={overviewQuery.data.recent_activity} />
              <HandoffsSection handoffs={overviewQuery.data.recent_handoffs} />
            </div>
          </div>
        </>
      )}
    </div>
  )
}

function BackButton({ onClick, label }: { onClick: () => void; label: string }) {
  return (
    <button
      type="button"
      onClick={onClick}
      aria-label={label}
      className="flex items-center gap-2 mb-6 text-body-sm transition-colors"
      style={{ color: 'var(--color-text-muted)', background: 'transparent', border: 'none', cursor: 'pointer', padding: 0 }}
      onMouseEnter={(e) => {
        e.currentTarget.style.color = 'var(--color-text-primary)'
      }}
      onMouseLeave={(e) => {
        e.currentTarget.style.color = 'var(--color-text-muted)'
      }}
    >
      <ArrowLeft size={16} aria-hidden="true" />
      {label}
    </button>
  )
}
