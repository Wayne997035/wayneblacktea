import { useRef, useEffect, useState } from 'react'
import { X } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { useCreateGoal } from '../../hooks/useGoals'
import { useToastStore } from '../../stores/toastStore'

interface CreateGoalModalProps {
  onClose: () => void;
}

export function CreateGoalModal({ onClose }: CreateGoalModalProps) {
  const { t } = useTranslation()
  const dialogRef = useRef<HTMLDialogElement>(null)
  const { addToast } = useToastStore()
  const createGoal = useCreateGoal()

  const [title, setTitle] = useState('')
  const [area, setArea] = useState('')
  const [description, setDescription] = useState('')
  const [dueDate, setDueDate] = useState('')
  const [formError, setFormError] = useState('')

  useEffect(() => {
    const dialog = dialogRef.current
    if (dialog) {
      dialog.showModal()
      dialog.addEventListener('close', onClose)
    }
    return () => {
      if (dialog) {
        dialog.removeEventListener('close', onClose)
      }
    }
  }, [onClose])

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!title.trim()) {
      setFormError('Title is required')
      return
    }
    setFormError('')

    try {
      await createGoal.mutateAsync({
        title: title.trim(),
        area: area.trim() || undefined,
        description: description.trim() || undefined,
        due_date: dueDate || null,
      })
      addToast({ type: 'success', message: 'Goal created!' })
      dialogRef.current?.close()
    } catch {
      setFormError(t('error.loadFailed'))
    }
  }

  const handleBackdropClick = (e: React.MouseEvent<HTMLDialogElement>) => {
    if (e.target === dialogRef.current) {
      dialogRef.current?.close()
    }
  }

  return (
    <dialog
      ref={dialogRef}
      aria-modal="true"
      aria-labelledby="goal-modal-title"
      onClick={handleBackdropClick}
      style={{ background: 'transparent' }}
    >
      <div
        className="rounded-xl w-full p-6"
        style={{
          background: 'var(--color-bg-card)',
          border: '1px solid var(--color-border)',
          minWidth: 'min(400px, 90vw)',
          maxWidth: '448px',
        }}
      >
        <div className="flex items-center justify-between mb-5">
          <h2
            id="goal-modal-title"
            className="text-section"
            style={{ color: 'var(--color-text-primary)' }}
          >
            {t('gtd.addGoal')}
          </h2>
          <button
            type="button"
            onClick={() => dialogRef.current?.close()}
            aria-label="Close"
            className="flex items-center justify-center w-8 h-8 rounded-md transition-colors"
            style={{ color: 'var(--color-text-muted)' }}
            onMouseEnter={(e) => { e.currentTarget.style.background = 'var(--color-bg-hover)' }}
            onMouseLeave={(e) => { e.currentTarget.style.background = 'transparent' }}
          >
            <X size={16} aria-hidden="true" />
          </button>
        </div>

        <form onSubmit={(e) => { void handleSubmit(e) }} className="flex flex-col gap-4">
          <div>
            <label
              htmlFor="goal-title"
              className="text-label block mb-1"
              style={{ color: 'var(--color-text-muted)' }}
            >
              Title
            </label>
            <input
              id="goal-title"
              type="text"
              autoFocus
              value={title}
              onChange={(e) => setTitle(e.target.value)}
              placeholder="Goal title..."
              className="w-full rounded-md px-3 py-2 text-body"
              style={{
                background: 'var(--color-bg-input)',
                border: '1px solid var(--color-border)',
                color: 'var(--color-text-primary)',
                outline: 'none',
              }}
              onFocus={(e) => { e.currentTarget.style.borderColor = 'var(--color-border-focus)' }}
              onBlur={(e) => { e.currentTarget.style.borderColor = 'var(--color-border)' }}
            />
          </div>

          <div>
            <label
              htmlFor="goal-area"
              className="text-label block mb-1"
              style={{ color: 'var(--color-text-muted)' }}
            >
              Area (optional)
            </label>
            <input
              id="goal-area"
              type="text"
              value={area}
              onChange={(e) => setArea(e.target.value)}
              placeholder="e.g. Career, Health..."
              className="w-full rounded-md px-3 py-2 text-body"
              style={{
                background: 'var(--color-bg-input)',
                border: '1px solid var(--color-border)',
                color: 'var(--color-text-primary)',
                outline: 'none',
              }}
              onFocus={(e) => { e.currentTarget.style.borderColor = 'var(--color-border-focus)' }}
              onBlur={(e) => { e.currentTarget.style.borderColor = 'var(--color-border)' }}
            />
          </div>

          <div>
            <label
              htmlFor="goal-description"
              className="text-label block mb-1"
              style={{ color: 'var(--color-text-muted)' }}
            >
              Description (optional)
            </label>
            <textarea
              id="goal-description"
              value={description}
              onChange={(e) => setDescription(e.target.value)}
              placeholder="What does achieving this goal look like?"
              rows={3}
              className="w-full rounded-md px-3 py-2 text-body resize-none"
              style={{
                background: 'var(--color-bg-input)',
                border: '1px solid var(--color-border)',
                color: 'var(--color-text-primary)',
                outline: 'none',
              }}
              onFocus={(e) => { e.currentTarget.style.borderColor = 'var(--color-border-focus)' }}
              onBlur={(e) => { e.currentTarget.style.borderColor = 'var(--color-border)' }}
            />
          </div>

          <div>
            <label
              htmlFor="goal-due"
              className="text-label block mb-1"
              style={{ color: 'var(--color-text-muted)' }}
            >
              Due date (optional)
            </label>
            <input
              id="goal-due"
              type="date"
              value={dueDate}
              onChange={(e) => setDueDate(e.target.value)}
              className="w-full rounded-md px-3 py-2 text-body"
              style={{
                background: 'var(--color-bg-input)',
                border: '1px solid var(--color-border)',
                color: 'var(--color-text-primary)',
                colorScheme: 'dark',
              }}
            />
          </div>

          {formError && (
            <p className="text-body-sm" style={{ color: 'var(--color-error)' }}>{formError}</p>
          )}

          <div className="flex gap-3 mt-2">
            <button
              type="button"
              onClick={() => dialogRef.current?.close()}
              className="flex-1 py-2 rounded-md text-body transition-colors"
              style={{
                border: '1px solid var(--color-border)',
                color: 'var(--color-text-muted)',
                background: 'transparent',
              }}
            >
              {t('common.cancel')}
            </button>
            <button
              type="submit"
              disabled={createGoal.isPending}
              className="flex-1 py-2 rounded-md text-body font-medium transition-colors"
              style={{
                background: 'var(--color-accent-blue)',
                color: 'var(--color-bg-base)',
                border: 'none',
                cursor: createGoal.isPending ? 'not-allowed' : 'pointer',
                opacity: createGoal.isPending ? 0.7 : 1,
              }}
            >
              {createGoal.isPending ? '...' : t('gtd.addGoal')}
            </button>
          </div>
        </form>
      </div>
    </dialog>
  )
}
