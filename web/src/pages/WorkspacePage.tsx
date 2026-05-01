import { useTranslation } from 'react-i18next'
import { RefreshCw } from 'lucide-react'
import { useRepos, useRefreshRepos } from '../hooks/useRepos'
import { RepoCard } from '../components/workspace/RepoCard'
import { LoadingSkeleton } from '../components/ui/LoadingSkeleton'
import { EmptyState } from '../components/ui/EmptyState'

export function WorkspacePage() {
  const { t } = useTranslation()
  const { data: repos, isLoading, isError, isFetching } = useRepos()
  const refreshRepos = useRefreshRepos()

  return (
    <div className="p-6 max-w-[1200px] mx-auto">
      <div className="flex items-center justify-between mb-6">
        <h1 className="text-page-title" style={{ color: 'var(--color-text-primary)' }}>
          {t('workspace.title')}
        </h1>
        <button
          type="button"
          onClick={() => void refreshRepos()}
          disabled={isFetching}
          className="flex items-center gap-2 px-4 py-2 rounded-md text-body-sm transition-colors"
          style={{
            border: '1px solid var(--color-accent-blue)',
            color: 'var(--color-accent-blue)',
            background: 'transparent',
            cursor: isFetching ? 'not-allowed' : 'pointer',
            opacity: isFetching ? 0.7 : 1,
          }}
        >
          <RefreshCw
            size={14}
            aria-hidden="true"
            style={{ animation: isFetching ? 'spin 1s linear infinite' : 'none' }}
          />
          {t('workspace.syncRepos')}
        </button>
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
        <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-4">
          {Array.from({ length: 6 }, (_, i) => (
            <LoadingSkeleton key={i} className="h-28 w-full" />
          ))}
        </div>
      ) : !repos || repos.length === 0 ? (
        <EmptyState messageKey="workspace.noRepos" />
      ) : (
        <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-4">
          {repos.map((repo) => (
            <RepoCard key={repo.id} repo={repo} />
          ))}
        </div>
      )}

      <style>{`@keyframes spin { to { transform: rotate(360deg); } }`}</style>
    </div>
  )
}
