import { useTranslation } from 'react-i18next'
import type { SearchResult } from '../../types/api'

interface TypeBadgeProps {
  type: SearchResult['type']
}

function TypeBadge({ type }: TypeBadgeProps) {
  const { t } = useTranslation()

  const colorMap: Record<SearchResult['type'], string> = {
    knowledge: 'var(--color-accent-blue)',
    decision: 'var(--color-accent-purple)',
    task: 'var(--color-success)',
    project: 'var(--color-accent-amber, #f59e0b)',
  }

  return (
    <span
      className="text-label shrink-0 px-1.5 py-0.5 rounded"
      style={{
        color: colorMap[type],
        background: `${colorMap[type]}22`,
        border: `1px solid ${colorMap[type]}44`,
      }}
    >
      {t(`search.type.${type}`)}
    </span>
  )
}

export interface SearchResultItemProps {
  result: SearchResult
  isActive: boolean
  onSelect: (result: SearchResult) => void
  onMouseEnter: () => void
}

export function SearchResultItem({ result, isActive, onSelect, onMouseEnter }: SearchResultItemProps) {
  const preview = result.snippet.slice(0, 120)
  const hasMore = result.snippet.length > 120

  return (
    <button
      type="button"
      role="option"
      id={`search-result-${result.id}`}
      aria-selected={isActive}
      className="w-full text-left px-4 py-3 flex flex-col gap-1 transition-colors"
      style={{
        background: isActive ? 'var(--color-bg-hover)' : 'transparent',
        borderBottom: '1px solid var(--color-border)',
        cursor: 'pointer',
      }}
      onClick={() => onSelect(result)}
      onMouseEnter={onMouseEnter}
    >
      <div className="flex items-center gap-2 min-w-0">
        <TypeBadge type={result.type} />
        <span
          className="text-card-title truncate"
          style={{ color: 'var(--color-text-primary)' }}
        >
          {result.title}
        </span>
      </div>
      {/* snippet is plain text — React escapes it by default, no XSS risk */}
      <p
        className="text-caption line-clamp-2"
        style={{ color: 'var(--color-text-muted)' }}
      >
        {preview}{hasMore ? '…' : ''}
      </p>
    </button>
  )
}
