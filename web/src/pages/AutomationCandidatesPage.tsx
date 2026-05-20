import { useTranslation } from 'react-i18next'
import { CandidateRow } from '../features/completion-candidates/CandidateRow'
import { useCompletionCandidates } from '../features/completion-candidates/api'
import { LoadingSkeleton } from '../components/ui/LoadingSkeleton'
import { EmptyState } from '../components/ui/EmptyState'

export function AutomationCandidatesPage() {
  const { t } = useTranslation()
  const { data, isLoading, isError } = useCompletionCandidates()

  return (
    <div className="p-6 max-w-[1200px] mx-auto">
      <header className="mb-6 flex flex-col gap-1">
        <h1 className="text-section" style={{ color: 'var(--color-text-primary)' }}>
          {t('candidates.title')}
        </h1>
        <p className="text-body" style={{ color: 'var(--color-text-muted)' }}>
          {t('candidates.subtitle')}
        </p>
      </header>

      {isLoading ? (
        <div className="flex flex-col gap-3">
          {Array.from({ length: 3 }, (_, i) => (
            <LoadingSkeleton key={i} className="w-full" style={{ height: '120px' }} />
          ))}
        </div>
      ) : isError ? (
        <div
          className="rounded-md p-3 text-body-sm"
          style={{
            background: '#2e0a0a',
            border: '1px solid var(--color-error)',
            color: 'var(--color-error)',
          }}
        >
          {t('candidates.error')}
        </div>
      ) : !data || data.length === 0 ? (
        <EmptyState messageKey="candidates.empty" />
      ) : (
        <div className="flex flex-col gap-3">
          {data.map((candidate) => (
            <CandidateRow key={candidate.id} candidate={candidate} />
          ))}
        </div>
      )}
    </div>
  )
}
