import { useState } from 'react'
import type { ChangeEvent } from 'react'
import { useTranslation } from 'react-i18next'
import { RefreshCw } from 'lucide-react'
import { useRepos, useRefreshRepos } from '../hooks/useRepos'
import { useWorkspaceSettings, useUpdateWorkspaceSettings } from '../hooks/useWorkspaceSettings'
import { ALLOWED_MODELS } from '../types/api'
import { RepoCard } from '../components/workspace/RepoCard'
import { LoadingSkeleton } from '../components/ui/LoadingSkeleton'
import { EmptyState } from '../components/ui/EmptyState'

const MODEL_LABELS: Record<string, string> = {
  'claude-haiku-4-5': 'Haiku 4.5',
  'claude-sonnet-4-6': 'Sonnet 4.6',
  'claude-opus-4-8': 'Opus 4.8',
}

export function WorkspacePage() {
  const { t } = useTranslation()
  const { data: repos, isLoading, isError, isFetching } = useRepos()
  const refreshRepos = useRefreshRepos()
  const { data: settings } = useWorkspaceSettings()
  const updateSettings = useUpdateWorkspaceSettings()
  const [saveState, setSaveState] = useState<'ok' | 'err' | null>(null)

  const onModelChange = (e: ChangeEvent<HTMLSelectElement>) => {
    setSaveState(null)
    updateSettings.mutate(
      { model_preference: e.target.value },
      { onSuccess: () => setSaveState('ok'), onError: () => setSaveState('err') },
    )
  }

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

      <section
        className="rounded-md p-4 mb-6"
        style={{ background: 'var(--color-bg-card)', border: '1px solid var(--color-border)' }}
        aria-label={t('workspace.settings.title')}
      >
        <h2 className="text-body-sm mb-2" style={{ color: 'var(--color-text-primary)' }}>
          {t('workspace.settings.title')}
        </h2>
        <div className="flex items-center gap-3 flex-wrap">
          <label htmlFor="model-pref" className="text-body-sm" style={{ color: 'var(--color-text-muted)' }}>
            {t('workspace.settings.modelLabel')}
          </label>
          <select
            id="model-pref"
            value={settings?.model_preference ?? ALLOWED_MODELS[1]}
            onChange={onModelChange}
            disabled={updateSettings.isPending}
            className="px-3 py-2 rounded-md text-body-sm"
            style={{
              background: 'var(--color-bg-input)',
              color: 'var(--color-text-primary)',
              border: '1px solid var(--color-border)',
              cursor: updateSettings.isPending ? 'not-allowed' : 'pointer',
            }}
          >
            {ALLOWED_MODELS.map((m) => (
              <option key={m} value={m}>
                {MODEL_LABELS[m] ?? m}
              </option>
            ))}
          </select>
          {saveState === 'ok' && (
            <span className="text-body-sm" style={{ color: 'var(--color-success)' }}>
              {t('workspace.settings.saved')}
            </span>
          )}
          {saveState === 'err' && (
            <span className="text-body-sm" style={{ color: 'var(--color-error)' }}>
              {t('workspace.settings.saveFailed')}
            </span>
          )}
        </div>
        <p className="text-body-sm mt-2" style={{ color: 'var(--color-text-muted)' }}>
          {t('workspace.settings.modelHint')}
        </p>
      </section>

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
