import { useState, useRef } from 'react'
import { useTranslation } from 'react-i18next'
import { GraduationCap, Plus, X, Sparkles } from 'lucide-react'
import { useReviews, useCreateConcept, useLearningSuggestions, useCreateConceptFromKnowledge } from '../hooks/useReviews'
import { ReviewCard } from '../components/reviews/ReviewCard'
import { LoadingSkeleton } from '../components/ui/LoadingSkeleton'
import { EmptyState } from '../components/ui/EmptyState'
import type { LearningSuggestion } from '../types/api'

interface AddConceptForm {
  title: string
  content: string
  tags: string
}

const EMPTY_FORM: AddConceptForm = { title: '', content: '', tags: '' }
const MAX_SUGGESTIONS_SHOWN = 5

interface SuggestionItemProps {
  suggestion: LearningSuggestion
  kind: 'knowledge' | 'decision'
  onAdd: () => void
  isPending: boolean
}

function SuggestionItem({ suggestion, kind, onAdd, isPending }: SuggestionItemProps) {
  const [added, setAdded] = useState(false)

  function handleAdd() {
    onAdd()
    setAdded(true)
    setTimeout(() => setAdded(false), 1000)
  }

  return (
    <div
      className="flex items-center gap-3 rounded-md px-3 py-2"
      style={{
        background: 'var(--color-bg-hover)',
        border: '1px solid var(--color-border)',
      }}
    >
      <span
        className="text-label rounded px-1.5 py-0.5 shrink-0"
        style={{
          background: kind === 'knowledge' ? 'rgba(79,195,247,0.1)' : 'rgba(167,139,250,0.1)',
          color: kind === 'knowledge' ? 'var(--color-accent-blue)' : '#a78bfa',
          border: `1px solid ${kind === 'knowledge' ? 'var(--color-accent-blue)' : '#a78bfa'}`,
          fontSize: '0.7rem',
        }}
      >
        {kind === 'knowledge' ? 'Knowledge' : 'Decision'}
      </span>
      <span
        className="text-body-sm flex-1 min-w-0 truncate"
        style={{ color: 'var(--color-text-primary)' }}
        title={suggestion.title}
      >
        {suggestion.title}
      </span>
      <button
        type="button"
        onClick={handleAdd}
        disabled={isPending || added}
        aria-label={`加入學習：${suggestion.title}`}
        className="text-label rounded px-2 py-0.5 shrink-0 transition-opacity"
        style={{
          minHeight: '28px',
          background: added ? 'rgba(34,197,94,0.1)' : 'transparent',
          color: added ? '#22c55e' : 'var(--color-accent-blue)',
          border: `1px solid ${added ? '#22c55e' : 'var(--color-accent-blue)'}`,
          cursor: isPending || added ? 'not-allowed' : 'pointer',
          opacity: isPending ? 0.5 : 1,
          whiteSpace: 'nowrap',
        }}
      >
        {added ? '已加入' : isPending ? '加入中…' : '加入學習'}
      </button>
    </div>
  )
}

function SuggestionsPanel() {
  const { data: suggestions, isLoading, isError } = useLearningSuggestions()
  const addFromKnowledge = useCreateConceptFromKnowledge()
  const createConcept = useCreateConcept()
  const [expanded, setExpanded] = useState(false)

  if (isError || isLoading) return null

  const allItems = [
    ...(suggestions?.knowledge_items ?? []).map((s) => ({ ...s, kind: 'knowledge' as const })),
    ...(suggestions?.decisions ?? []).map((s) => ({ ...s, kind: 'decision' as const })),
  ]

  if (allItems.length === 0) return null

  const visibleItems = expanded ? allItems : allItems.slice(0, MAX_SUGGESTIONS_SHOWN)
  const hasMore = allItems.length > MAX_SUGGESTIONS_SHOWN

  return (
    <div
      className="rounded-lg p-4 mb-4"
      style={{
        background: 'var(--color-bg-card)',
        border: '1px solid var(--color-border)',
      }}
    >
      <div className="flex items-center gap-2 mb-3">
        <Sparkles size={15} aria-hidden="true" style={{ color: 'var(--color-accent-blue)' }} />
        <h2 className="text-card-title" style={{ color: 'var(--color-text-primary)' }}>
          AI 推薦
        </h2>
        <span
          className="text-label rounded-full px-2 py-0.5 ml-auto"
          style={{
            background: 'var(--color-bg-hover)',
            color: 'var(--color-text-muted)',
            border: '1px solid var(--color-border)',
          }}
        >
          {allItems.length} 項
        </span>
      </div>

      <div className="flex flex-col gap-2">
        {visibleItems.map((item) => (
          <SuggestionItem
            key={`${item.kind}-${item.id}`}
            suggestion={item}
            kind={item.kind}
            isPending={addFromKnowledge.isPending || createConcept.isPending}
            onAdd={() => {
              if (item.kind === 'knowledge') {
                addFromKnowledge.mutate({ knowledge_id: item.id })
              } else {
                createConcept.mutate({
                  title: item.title,
                  content: item.context ?? item.content,
                  tags: [],
                })
              }
            }}
          />
        ))}
      </div>

      {hasMore && (
        <button
          type="button"
          onClick={() => setExpanded((prev) => !prev)}
          className="text-body-sm mt-3 w-full text-center transition-colors"
          style={{
            background: 'transparent',
            border: 'none',
            color: 'var(--color-accent-blue)',
            cursor: 'pointer',
            padding: '4px 0',
          }}
        >
          {expanded ? '收起' : `顯示全部 ${allItems.length} 項`}
        </button>
      )}
    </div>
  )
}

export function ReviewsPage() {
  const { t } = useTranslation()
  const { data: reviews, isLoading, isError } = useReviews()
  const createConcept = useCreateConcept()

  const [formOpen, setFormOpen] = useState(false)
  const [form, setForm] = useState<AddConceptForm>(EMPTY_FORM)
  const titleRef = useRef<HTMLInputElement>(null)

  const dueCount = reviews?.length ?? 0

  function handleOpenForm() {
    setFormOpen(true)
    // Focus title input after open on next tick
    setTimeout(() => titleRef.current?.focus(), 50)
  }

  function handleCloseForm() {
    setFormOpen(false)
    setForm(EMPTY_FORM)
  }

  function handleFormChange(field: keyof AddConceptForm) {
    return (e: React.ChangeEvent<HTMLInputElement | HTMLTextAreaElement>) => {
      setForm((prev) => ({ ...prev, [field]: e.target.value }))
    }
  }

  function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    if (!form.title.trim() || !form.content.trim()) return

    const tags = form.tags
      .split(',')
      .map((t) => t.trim())
      .filter(Boolean)

    createConcept.mutate(
      { title: form.title.trim(), content: form.content.trim(), tags },
      {
        onSuccess: () => {
          handleCloseForm()
        },
      },
    )
  }

  const inputStyle: React.CSSProperties = {
    background: 'var(--color-bg-input)',
    border: '1px solid var(--color-border)',
    color: 'var(--color-text-primary)',
    borderRadius: '6px',
    padding: '10px 12px',
    fontSize: '0.875rem',
    width: '100%',
    outline: 'none',
    boxSizing: 'border-box',
  }

  return (
    <div className="p-4 sm:p-6 max-w-[800px] mx-auto pb-24">
      {/* Page header */}
      <div className="flex items-center justify-between mb-4 gap-2">
        <div className="min-w-0">
          <h1 className="text-page-title" style={{ color: 'var(--color-text-primary)' }}>
            {t('reviews.title')}
          </h1>
          {!isLoading && (
            <p className="text-body-sm mt-0.5" style={{ color: 'var(--color-text-muted)' }}>
              {t('reviews.dueCount', { count: dueCount })}
            </p>
          )}
        </div>
        <button
          type="button"
          onClick={formOpen ? handleCloseForm : handleOpenForm}
          aria-label={t('reviews.addConcept')}
          aria-expanded={formOpen}
          className="flex items-center gap-2 rounded-md px-3 shrink-0 transition-colors"
          style={{
            minHeight: '44px',
            background: 'var(--color-bg-card)',
            border: '1px solid var(--color-border)',
            color: 'var(--color-accent-blue)',
            cursor: 'pointer',
            fontSize: '0.875rem',
            fontWeight: 500,
          }}
        >
          {formOpen ? (
            <X size={16} aria-hidden="true" />
          ) : (
            <Plus size={16} aria-hidden="true" />
          )}
          <span>{t('reviews.addConcept')}</span>
        </button>
      </div>

      {/* Inline Add Concept form */}
      {formOpen && (
        <div
          className="rounded-lg p-4 mb-4"
          style={{
            background: 'var(--color-bg-card)',
            border: '1px solid var(--color-border)',
          }}
        >
          <div className="flex items-center justify-between mb-3">
            <h2 className="text-card-title" style={{ color: 'var(--color-text-primary)' }}>
              {t('reviews.addConcept')}
            </h2>
            <button
              type="button"
              onClick={handleCloseForm}
              aria-label={t('common.cancel')}
              className="flex items-center justify-center rounded-md"
              style={{
                minHeight: '44px',
                minWidth: '44px',
                background: 'transparent',
                border: 'none',
                color: 'var(--color-text-muted)',
                cursor: 'pointer',
              }}
            >
              <X size={18} aria-hidden="true" />
            </button>
          </div>

          <form onSubmit={handleSubmit} noValidate>
            <div className="flex flex-col gap-3">
              {/* Title */}
              <div>
                <label
                  htmlFor="concept-title"
                  className="text-body-sm block mb-1"
                  style={{ color: 'var(--color-text-muted)' }}
                >
                  {t('reviews.form.title')}
                  <span style={{ color: 'var(--color-error)' }}> *</span>
                </label>
                <input
                  id="concept-title"
                  ref={titleRef}
                  type="text"
                  value={form.title}
                  onChange={handleFormChange('title')}
                  placeholder={t('reviews.form.titlePlaceholder')}
                  required
                  style={inputStyle}
                  onFocus={(e) => { e.currentTarget.style.borderColor = 'var(--color-border-focus)' }}
                  onBlur={(e) => { e.currentTarget.style.borderColor = 'var(--color-border)' }}
                />
              </div>

              {/* Content */}
              <div>
                <label
                  htmlFor="concept-content"
                  className="text-body-sm block mb-1"
                  style={{ color: 'var(--color-text-muted)' }}
                >
                  {t('reviews.form.content')}
                  <span style={{ color: 'var(--color-error)' }}> *</span>
                </label>
                <textarea
                  id="concept-content"
                  value={form.content}
                  onChange={handleFormChange('content')}
                  placeholder={t('reviews.form.contentPlaceholder')}
                  required
                  rows={4}
                  style={{ ...inputStyle, resize: 'vertical', minHeight: '88px' }}
                  onFocus={(e) => { e.currentTarget.style.borderColor = 'var(--color-border-focus)' }}
                  onBlur={(e) => { e.currentTarget.style.borderColor = 'var(--color-border)' }}
                />
              </div>

              {/* Tags */}
              <div>
                <label
                  htmlFor="concept-tags"
                  className="text-body-sm block mb-1"
                  style={{ color: 'var(--color-text-muted)' }}
                >
                  {t('reviews.form.tags')}
                </label>
                <input
                  id="concept-tags"
                  type="text"
                  value={form.tags}
                  onChange={handleFormChange('tags')}
                  placeholder={t('reviews.form.tagsPlaceholder')}
                  style={inputStyle}
                  onFocus={(e) => { e.currentTarget.style.borderColor = 'var(--color-border-focus)' }}
                  onBlur={(e) => { e.currentTarget.style.borderColor = 'var(--color-border)' }}
                />
              </div>

              {/* Actions */}
              <div className="flex gap-2 justify-end">
                <button
                  type="button"
                  onClick={handleCloseForm}
                  className="rounded-md px-4 text-body transition-colors"
                  style={{
                    minHeight: '44px',
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
                  disabled={createConcept.isPending || !form.title.trim() || !form.content.trim()}
                  className="rounded-md px-4 text-body font-medium transition-opacity"
                  style={{
                    minHeight: '44px',
                    background: 'var(--color-accent-blue)',
                    border: 'none',
                    color: 'var(--color-bg-base)',
                    cursor: createConcept.isPending ? 'not-allowed' : 'pointer',
                    opacity: createConcept.isPending || !form.title.trim() || !form.content.trim() ? 0.45 : 1,
                  }}
                >
                  {createConcept.isPending ? t('common.loading') : t('common.add')}
                </button>
              </div>

              {createConcept.isError && (
                <p className="text-body-sm" style={{ color: 'var(--color-error)' }}>
                  {t('error.loadFailed')}
                </p>
              )}
            </div>
          </form>
        </div>
      )}

      {/* AI Suggestions panel */}
      <SuggestionsPanel />

      {/* Error state */}
      {isError && (
        <div
          className="rounded-md p-3 mb-4 text-body-sm"
          style={{
            background: '#2e0a0a',
            border: '1px solid var(--color-error)',
            color: 'var(--color-error)',
          }}
        >
          {t('error.loadFailed')}
        </div>
      )}

      {/* Loading */}
      {isLoading ? (
        <div className="flex flex-col gap-3">
          {Array.from({ length: 3 }, (_, i) => (
            <LoadingSkeleton key={i} className="w-full" style={{ height: '160px' }} />
          ))}
        </div>
      ) : dueCount === 0 ? (
        <EmptyState
          icon={GraduationCap}
          messageKey="reviews.noDue"
          ctaLabelKey="reviews.addConcept"
          onCta={handleOpenForm}
        />
      ) : (
        <div className="flex flex-col gap-3">
          {(reviews ?? []).map((review) => (
            <ReviewCard key={review.schedule_id} review={review} />
          ))}
        </div>
      )}
    </div>
  )
}
