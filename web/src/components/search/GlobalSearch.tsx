import { useState, useEffect, useRef, useCallback, useMemo } from 'react'
import { createPortal } from 'react-dom'
import { useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { Search, X } from 'lucide-react'
import { useSearch } from '../../hooks/useSearch'
import type { SearchResult } from '../../types/api'

const MAX_QUERY_LENGTH = 500

const ROUTE_MAP: Record<SearchResult['type'], string> = {
  knowledge: '/knowledge',
  decision: '/decisions',
  task: '/gtd',
}

interface TypeBadgeProps {
  type: SearchResult['type']
}

function TypeBadge({ type }: TypeBadgeProps) {
  const { t } = useTranslation()
  const labelKey = `search.type.${type}` as const

  const colorMap: Record<SearchResult['type'], string> = {
    knowledge: 'var(--color-accent-blue)',
    decision: 'var(--color-accent-purple)',
    task: 'var(--color-success)',
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
      {t(labelKey)}
    </span>
  )
}

interface ResultItemProps {
  result: SearchResult
  isActive: boolean
  onSelect: (result: SearchResult) => void
  onMouseEnter: () => void
}

function ResultItem({ result, isActive, onSelect, onMouseEnter }: ResultItemProps) {
  const preview = result.content.slice(0, 120)
  const hasMore = result.content.length > 120

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
      <p
        className="text-caption line-clamp-2"
        style={{ color: 'var(--color-text-muted)' }}
      >
        {preview}{hasMore ? '…' : ''}
      </p>
    </button>
  )
}

interface GlobalSearchProps {
  isOpen: boolean
  onClose: () => void
}

export function GlobalSearch({ isOpen, onClose }: GlobalSearchProps) {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const inputRef = useRef<HTMLInputElement>(null)
  const listRef = useRef<HTMLDivElement>(null)

  const [inputValue, setInputValue] = useState('')
  const [debouncedQuery, setDebouncedQuery] = useState('')
  const [activeIndex, setActiveIndex] = useState(0)

  // Debounce: 300ms
  useEffect(() => {
    const id = setTimeout(() => setDebouncedQuery(inputValue), 300)
    return () => clearTimeout(id)
  }, [inputValue])

  // Reset state when palette opens
  useEffect(() => {
    if (isOpen) {
      setInputValue('')
      setDebouncedQuery('')
      setActiveIndex(0)
      // Focus input on next tick (after portal mounts)
      requestAnimationFrame(() => inputRef.current?.focus())
    }
  }, [isOpen])

  const { data, isFetching } = useSearch(debouncedQuery)
  const results = useMemo(() => data?.results ?? [], [data?.results])

  // Reset active index when results change
  useEffect(() => {
    setActiveIndex(0)
  }, [results.length])

  const handleSelect = useCallback(
    (result: SearchResult) => {
      navigate(ROUTE_MAP[result.type])
      onClose()
    },
    [navigate, onClose],
  )

  const handleKeyDown = useCallback(
    (e: React.KeyboardEvent) => {
      if (e.key === 'ArrowDown') {
        e.preventDefault()
        setActiveIndex((i) => Math.min(i + 1, results.length - 1))
      } else if (e.key === 'ArrowUp') {
        e.preventDefault()
        setActiveIndex((i) => Math.max(i - 1, 0))
      } else if (e.key === 'Enter' && results[activeIndex]) {
        e.preventDefault()
        handleSelect(results[activeIndex])
      } else if (e.key === 'Escape') {
        e.stopPropagation()
        onClose()
      }
    },
    [results, activeIndex, handleSelect, onClose],
  )

  const handleInputChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    // Enforce max 500 char limit; truncate rather than reject
    const val = e.target.value.slice(0, MAX_QUERY_LENGTH)
    setInputValue(val)
  }

  if (!isOpen) return null

  const showEmpty = debouncedQuery.length === 0
  const showNoResults = debouncedQuery.length >= 1 && !isFetching && results.length === 0

  const palette = (
    // Backdrop
    <div
      className="fixed inset-0 flex items-start justify-center"
      style={{
        zIndex: 70,
        background: 'var(--color-bg-overlay)',
        paddingTop: 'calc(var(--spacing-header) + 48px)',
      }}
      onClick={onClose}
      aria-hidden="true"
    >
      {/* Palette panel — stop propagation so clicks inside don't close */}
      <div
        role="dialog"
        aria-modal="true"
        aria-label={t('search.placeholder')}
        className="flex flex-col rounded-xl overflow-hidden w-full max-w-xl mx-4"
        style={{
          background: 'var(--color-bg-card)',
          border: '1px solid var(--color-border)',
          boxShadow: '0 24px 48px rgba(0,0,0,0.6)',
          maxHeight: '60vh',
        }}
        onClick={(e) => e.stopPropagation()}
      >
        {/* Input row */}
        <div
          className="flex items-center gap-3 px-4"
          style={{
            borderBottom: '1px solid var(--color-border)',
            height: '52px',
          }}
        >
          <Search
            size={18}
            aria-hidden="true"
            style={{ color: 'var(--color-text-muted)', flexShrink: 0 }}
          />
          <input
            ref={inputRef}
            type="text"
            role="combobox"
            aria-autocomplete="list"
            aria-expanded={results.length > 0}
            aria-controls="search-results-list"
            aria-activedescendant={
              results[activeIndex] ? `search-result-${results[activeIndex].id}` : undefined
            }
            value={inputValue}
            onChange={handleInputChange}
            onKeyDown={handleKeyDown}
            placeholder={t('search.placeholder')}
            className="flex-1 bg-transparent outline-none text-body"
            style={{ color: 'var(--color-text-primary)' }}
            maxLength={MAX_QUERY_LENGTH}
          />
          {isFetching && (
            <span
              className="text-caption"
              style={{ color: 'var(--color-text-muted)', flexShrink: 0 }}
              aria-live="polite"
              aria-label={t('common.loading')}
            >
              {t('common.loading')}
            </span>
          )}
          <button
            type="button"
            aria-label="Close search"
            onClick={onClose}
            className="flex items-center justify-center rounded"
            style={{
              color: 'var(--color-text-muted)',
              width: '28px',
              height: '28px',
              flexShrink: 0,
            }}
          >
            <X size={16} aria-hidden="true" />
          </button>
        </div>

        {/* Results area */}
        <div
          ref={listRef}
          id="search-results-list"
          role="listbox"
          aria-label={t('search.placeholder')}
          className="overflow-y-auto flex-1"
        >
          {showEmpty && (
            <p
              className="px-4 py-6 text-center text-body-sm"
              style={{ color: 'var(--color-text-muted)' }}
            >
              {t('search.empty')}
            </p>
          )}

          {showNoResults && (
            <p
              className="px-4 py-6 text-center text-body-sm"
              style={{ color: 'var(--color-text-muted)' }}
            >
              {t('search.noResults')}
            </p>
          )}

          {results.map((result, index) => (
            <ResultItem
              key={`${result.type}-${result.id}`}
              result={result}
              isActive={index === activeIndex}
              onSelect={handleSelect}
              onMouseEnter={() => setActiveIndex(index)}
            />
          ))}
        </div>
      </div>
    </div>
  )

  return createPortal(palette, document.body)
}
