import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Search } from 'lucide-react'
import { useDecisions } from '../hooks/useDecisions'
import { useProjects } from '../hooks/useProjects'
import { DecisionTimeline } from '../components/decisions/DecisionTimeline'
import { LoadingSkeleton } from '../components/ui/LoadingSkeleton'
import { EmptyState } from '../components/ui/EmptyState'

export function DecisionsPage() {
  const { t } = useTranslation()
  const { data: decisions, isLoading, isError } = useDecisions()
  const { data: projects } = useProjects()
  const [projectFilter, setProjectFilter] = useState<string>('all')
  const [search, setSearch] = useState('')

  const filtered = (decisions ?? []).filter((d) => {
    const matchProject = projectFilter === 'all' || d.project_id === projectFilter
    const q = search.toLowerCase()
    const matchSearch = !q || d.title.toLowerCase().includes(q) || d.rationale.toLowerCase().includes(q)
    return matchProject && matchSearch
  })

  return (
    <div className="p-6 max-w-[1200px] mx-auto">
      <h1 className="text-page-title mb-4" style={{ color: 'var(--color-text-primary)' }}>
        {t('decisions.title')}
      </h1>

      {/* Filter row */}
      <div className="flex flex-wrap items-center gap-3 mb-6">
        <select
          value={projectFilter}
          onChange={(e) => setProjectFilter(e.target.value)}
          className="rounded-md px-3 py-2 text-body h-9"
          style={{
            background: 'var(--color-bg-input)',
            border: '1px solid var(--color-border)',
            color: 'var(--color-text-primary)',
          }}
          aria-label={t('decisions.allProjects')}
        >
          <option value="all">{t('decisions.allProjects')}</option>
          {(projects ?? []).map((p) => (
            <option key={p.id} value={p.id}>{p.title}</option>
          ))}
        </select>

        <div className="relative">
          <Search
            size={14}
            aria-hidden="true"
            className="absolute left-3 top-1/2 -translate-y-1/2"
            style={{ color: 'var(--color-text-muted)' }}
          />
          <input
            type="text"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            placeholder={t('decisions.searchPlaceholder')}
            className="rounded-md pl-9 pr-3 py-2 h-9 text-body"
            style={{
              background: 'var(--color-bg-input)',
              border: '1px solid var(--color-border)',
              color: 'var(--color-text-primary)',
              width: '224px',
              outline: 'none',
            }}
            onFocus={(e) => { e.currentTarget.style.borderColor = 'var(--color-border-focus)' }}
            onBlur={(e) => { e.currentTarget.style.borderColor = 'var(--color-border)' }}
            aria-label={t('decisions.searchPlaceholder')}
          />
        </div>
      </div>

      {isError && (
        <div
          className="rounded-md p-3 mb-6 text-body-sm"
          style={{
            background: '#2e0a0a',
            border: '1px solid var(--color-error)',
            color: 'var(--color-error)',
          }}
        >
          {t('error.loadFailed')}
        </div>
      )}

      {isLoading ? (
        <div className="ml-4 pl-4 border-l-2 space-y-6" style={{ borderColor: 'var(--color-border)' }}>
          {Array.from({ length: 4 }, (_, i) => (
            <LoadingSkeleton key={i} className="h-20 w-full" />
          ))}
        </div>
      ) : filtered.length === 0 ? (
        <EmptyState messageKey="decisions.noDecisions" />
      ) : (
        <DecisionTimeline decisions={filtered} />
      )}
    </div>
  )
}
