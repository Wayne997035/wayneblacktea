import { useState, useEffect, useRef } from 'react'
import { useTranslation } from 'react-i18next'
import { Search, Plus, X, BookOpen } from 'lucide-react'
import { useKnowledge, useKnowledgeSearch, useCreateKnowledge } from '../hooks/useKnowledge'
import { useNewConcepts } from '../hooks/useReviews'
import { KnowledgeCard } from '../components/knowledge/KnowledgeCard'
import { LoadingSkeleton } from '../components/ui/LoadingSkeleton'
import { EmptyState } from '../components/ui/EmptyState'
import type { KnowledgeType, CreateKnowledgeRequest } from '../types/api'

const KNOWLEDGE_TYPES: KnowledgeType[] = ['article', 'til', 'bookmark', 'zettelkasten']
const TYPE_LABELS: Record<KnowledgeType, string> = {
  article: 'Article',
  til: 'TIL',
  bookmark: 'Bookmark',
  zettelkasten: 'Note',
}

function useDebounce<T>(value: T, delay: number): T {
  const [debounced, setDebounced] = useState(value)
  useEffect(() => {
    const timer = setTimeout(() => setDebounced(value), delay)
    return () => clearTimeout(timer)
  }, [value, delay])
  return debounced
}

const INPUT_STYLE: React.CSSProperties = {
  background: 'var(--color-bg-input)',
  border: '1px solid var(--color-border)',
  color: 'var(--color-text-primary)',
  outline: 'none',
}

interface AddFormState {
  type: KnowledgeType;
  title: string;
  content: string;
  url: string;
}

const EMPTY_FORM: AddFormState = { type: 'article', title: '', content: '', url: '' }

export function KnowledgePage() {
  const { t } = useTranslation()
  const [searchInput, setSearchInput] = useState('')
  const [showForm, setShowForm] = useState(false)
  const [form, setForm] = useState<AddFormState>(EMPTY_FORM)
  const firstFieldRef = useRef<HTMLSelectElement>(null)

  const debouncedSearch = useDebounce(searchInput, 350)
  const isSearching = debouncedSearch.trim().length > 0

  const { data: listData, isLoading: listLoading, isError: listError } = useKnowledge()
  const { data: searchData, isLoading: searchLoading, isError: searchError } = useKnowledgeSearch(debouncedSearch)
  const { data: newConcepts } = useNewConcepts()
  const createMutation = useCreateKnowledge()

  const items = isSearching ? (searchData ?? []) : (listData ?? [])
  const isLoading = isSearching ? searchLoading : listLoading
  const isError = isSearching ? searchError : listError

  function openForm() {
    setForm(EMPTY_FORM)
    setShowForm(true)
    setTimeout(() => firstFieldRef.current?.focus(), 50)
  }

  function closeForm() {
    setShowForm(false)
    setForm(EMPTY_FORM)
  }

  function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    if (!form.title.trim() || !form.content.trim()) return

    const body: CreateKnowledgeRequest = {
      type: form.type,
      title: form.title.trim(),
      content: form.content.trim(),
      url: form.url.trim() || null,
    }

    createMutation.mutate(body, {
      onSuccess: () => {
        closeForm()
      },
    })
  }

  return (
    <div className="p-6 max-w-[1200px] mx-auto">
      <div className="flex items-center justify-between mb-4">
        <h1 className="text-page-title" style={{ color: 'var(--color-text-primary)' }}>
          {t('knowledge.title')}
        </h1>
        <button
          type="button"
          onClick={openForm}
          className="flex items-center gap-2 rounded-md px-3 py-2 text-body-sm transition-colors"
          style={{
            background: 'var(--color-accent-blue)',
            color: 'var(--color-bg-base)',
            border: 'none',
            cursor: 'pointer',
          }}
          aria-label={t('knowledge.addEntry')}
        >
          <Plus size={14} aria-hidden="true" />
          {t('knowledge.addEntry')}
        </button>
      </div>

      {/* Search bar */}
      <div className="relative mb-6" style={{ maxWidth: '320px' }}>
        <Search
          size={14}
          aria-hidden="true"
          className="absolute left-3 top-1/2 -translate-y-1/2"
          style={{ color: 'var(--color-text-muted)' }}
        />
        <input
          type="text"
          value={searchInput}
          onChange={(e) => setSearchInput(e.target.value)}
          placeholder={t('knowledge.searchPlaceholder')}
          className="w-full rounded-md pl-9 pr-3 py-2 h-9 text-body"
          style={INPUT_STYLE}
          onFocus={(e) => { e.currentTarget.style.borderColor = 'var(--color-border-focus)' }}
          onBlur={(e) => { e.currentTarget.style.borderColor = 'var(--color-border)' }}
          aria-label={t('knowledge.searchPlaceholder')}
        />
      </div>

      {/* Add form */}
      {showForm && (
        <div
          className="rounded-lg p-5 mb-6"
          style={{
            background: 'var(--color-bg-card)',
            border: '1px solid var(--color-border)',
          }}
        >
          <div className="flex items-center justify-between mb-4">
            <h2 className="text-card-title" style={{ color: 'var(--color-text-primary)' }}>
              {t('knowledge.addEntry')}
            </h2>
            <button
              type="button"
              onClick={closeForm}
              className="rounded p-1 transition-colors"
              style={{ background: 'transparent', border: 'none', cursor: 'pointer', color: 'var(--color-text-muted)' }}
              aria-label={t('common.cancel')}
            >
              <X size={16} aria-hidden="true" />
            </button>
          </div>

          <form onSubmit={handleSubmit} noValidate>
            <div className="grid gap-4">
              {/* Type selector */}
              <div>
                <label
                  htmlFor="knowledge-type"
                  className="text-label block mb-1"
                  style={{ color: 'var(--color-text-muted)' }}
                >
                  {t('knowledge.form.type')}
                </label>
                <select
                  id="knowledge-type"
                  ref={firstFieldRef}
                  value={form.type}
                  onChange={(e) => setForm((f) => ({ ...f, type: e.target.value as KnowledgeType }))}
                  className="rounded-md px-3 py-2 text-body h-9 w-full"
                  style={INPUT_STYLE}
                >
                  {KNOWLEDGE_TYPES.map((t) => (
                    <option key={t} value={t}>{TYPE_LABELS[t]}</option>
                  ))}
                </select>
              </div>

              {/* Title */}
              <div>
                <label
                  htmlFor="knowledge-title"
                  className="text-label block mb-1"
                  style={{ color: 'var(--color-text-muted)' }}
                >
                  {t('knowledge.form.title')}
                </label>
                <input
                  id="knowledge-title"
                  type="text"
                  value={form.title}
                  onChange={(e) => setForm((f) => ({ ...f, title: e.target.value }))}
                  placeholder={t('knowledge.form.titlePlaceholder')}
                  className="rounded-md px-3 py-2 h-9 text-body w-full"
                  style={INPUT_STYLE}
                  onFocus={(e) => { e.currentTarget.style.borderColor = 'var(--color-border-focus)' }}
                  onBlur={(e) => { e.currentTarget.style.borderColor = 'var(--color-border)' }}
                  required
                />
              </div>

              {/* Content */}
              <div>
                <label
                  htmlFor="knowledge-content"
                  className="text-label block mb-1"
                  style={{ color: 'var(--color-text-muted)' }}
                >
                  {t('knowledge.form.content')}
                </label>
                <textarea
                  id="knowledge-content"
                  value={form.content}
                  onChange={(e) => setForm((f) => ({ ...f, content: e.target.value }))}
                  placeholder={t('knowledge.form.contentPlaceholder')}
                  rows={4}
                  className="rounded-md px-3 py-2 text-body w-full resize-y"
                  style={{ ...INPUT_STYLE, minHeight: '96px' }}
                  onFocus={(e) => { e.currentTarget.style.borderColor = 'var(--color-border-focus)' }}
                  onBlur={(e) => { e.currentTarget.style.borderColor = 'var(--color-border)' }}
                  required
                />
              </div>

              {/* URL (optional) */}
              <div>
                <label
                  htmlFor="knowledge-url"
                  className="text-label block mb-1"
                  style={{ color: 'var(--color-text-muted)' }}
                >
                  {t('knowledge.form.url')}
                </label>
                <input
                  id="knowledge-url"
                  type="url"
                  value={form.url}
                  onChange={(e) => setForm((f) => ({ ...f, url: e.target.value }))}
                  placeholder={t('knowledge.form.urlPlaceholder')}
                  className="rounded-md px-3 py-2 h-9 text-body w-full"
                  style={INPUT_STYLE}
                  onFocus={(e) => { e.currentTarget.style.borderColor = 'var(--color-border-focus)' }}
                  onBlur={(e) => { e.currentTarget.style.borderColor = 'var(--color-border)' }}
                />
              </div>
            </div>

            {/* Form error */}
            {createMutation.isError && (
              <p
                className="text-body-sm mt-3"
                style={{ color: 'var(--color-error)' }}
                role="alert"
              >
                {t('error.loadFailed')}
              </p>
            )}

            {/* Actions */}
            <div className="flex justify-end gap-3 mt-5">
              <button
                type="button"
                onClick={closeForm}
                className="rounded-md px-4 py-2 text-body-sm transition-colors"
                style={{
                  background: 'transparent',
                  border: '1px solid var(--color-border)',
                  color: 'var(--color-text-muted)',
                  cursor: 'pointer',
                }}
              >
                {t('common.cancel')}
              </button>
              <button
                type="submit"
                disabled={createMutation.isPending || !form.title.trim() || !form.content.trim()}
                className="rounded-md px-4 py-2 text-body-sm transition-opacity"
                style={{
                  background: 'var(--color-accent-blue)',
                  color: 'var(--color-bg-base)',
                  border: 'none',
                  cursor: createMutation.isPending ? 'not-allowed' : 'pointer',
                  opacity: createMutation.isPending ? 0.6 : 1,
                }}
              >
                {createMutation.isPending ? t('common.loading') : t('common.add')}
              </button>
            </div>
          </form>
        </div>
      )}

      {/* Pending Concept Proposals */}
      {newConcepts && newConcepts.length > 0 && (
        <section className="mb-6">
          <div className="flex items-center gap-2 mb-3">
            <BookOpen size={15} aria-hidden="true" style={{ color: 'var(--color-accent-blue)' }} />
            <h2 className="text-card-title" style={{ color: 'var(--color-text-primary)' }}>
              {t('knowledge.pendingConcepts')}
            </h2>
            <span
              className="text-label rounded-full px-2 py-0.5 ml-auto"
              style={{
                background: 'var(--color-bg-hover)',
                color: 'var(--color-text-muted)',
                border: '1px solid var(--color-border)',
              }}
            >
              {newConcepts.length}
            </span>
          </div>
          <div
            className="rounded-lg overflow-hidden"
            style={{ border: '1px solid var(--color-border)' }}
          >
            {newConcepts.map((concept, idx) => (
              <div
                key={concept.concept_id}
                className="px-4 py-3 text-body-sm"
                style={{
                  background: 'var(--color-bg-card)',
                  borderTop: idx > 0 ? '1px solid var(--color-border)' : 'none',
                }}
              >
                <div className="flex items-center justify-between gap-3">
                  <span
                    className="font-medium"
                    style={{ color: 'var(--color-text-primary)' }}
                  >
                    {concept.title}
                  </span>
                  <span
                    className="text-label rounded-full px-2 py-0.5 shrink-0"
                    style={{
                      background: 'rgba(79,195,247,0.1)',
                      color: 'var(--color-accent-blue)',
                      border: '1px solid var(--color-accent-blue)',
                    }}
                  >
                    {t('knowledge.newConcept')}
                  </span>
                </div>
                {concept.content && (
                  <p
                    className="mt-1"
                    style={{
                      color: 'var(--color-text-muted)',
                      display: '-webkit-box',
                      WebkitLineClamp: 2,
                      WebkitBoxOrient: 'vertical',
                      overflow: 'hidden',
                    }}
                  >
                    {concept.content}
                  </p>
                )}
              </div>
            ))}
          </div>
        </section>
      )}

      {/* Error banner */}
      {isError && (
        <div
          className="rounded-md p-3 mb-6 text-body-sm"
          style={{
            background: '#2e0a0a',
            border: '1px solid var(--color-error)',
            color: 'var(--color-error)',
          }}
          role="alert"
        >
          {t('error.loadFailed')}
        </div>
      )}

      {/* List */}
      {isLoading ? (
        <div className="grid gap-3">
          {Array.from({ length: 4 }, (_, i) => (
            <LoadingSkeleton key={i} className="h-28 w-full" />
          ))}
        </div>
      ) : items.length === 0 ? (
        <EmptyState
          messageKey="knowledge.noItems"
          ctaLabelKey="knowledge.addEntry"
          onCta={openForm}
        />
      ) : (
        <div className="grid gap-3">
          {items.map((item) => (
            <KnowledgeCard key={item.id} item={item} />
          ))}
        </div>
      )}
    </div>
  )
}
