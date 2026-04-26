import { useState, useRef } from 'react'
import { useTranslation } from 'react-i18next'
import { GraduationCap, Plus, X } from 'lucide-react'
import { useReviews, useCreateConcept } from '../hooks/useReviews'
import { ReviewCard } from '../components/reviews/ReviewCard'
import { LoadingSkeleton } from '../components/ui/LoadingSkeleton'
import { EmptyState } from '../components/ui/EmptyState'

interface AddConceptForm {
  title: string
  content: string
  tags: string
}

const EMPTY_FORM: AddConceptForm = { title: '', content: '', tags: '' }

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
