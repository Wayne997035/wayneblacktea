import { useEffect, useRef, useState } from 'react'
import { X } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { useCreateGoal } from '../../hooks/useGoals'
import { useToastStore } from '../../stores/toastStore'
import { FormField } from '../ui/FormField'
import { inputBaseStyle, inputDateStyle, onInputBlur, onInputFocus } from '../ui/formStyles'

interface GoalCreateModalProps {
  onClose: () => void;
}

interface FieldErrors {
  title?: string;
  area?: string;
  description?: string;
  dueDate?: string;
}

const TITLE_MAX = 200
const AREA_MAX = 80
const DESCRIPTION_MAX = 2000

export function GoalCreateModal({ onClose }: GoalCreateModalProps) {
  const { t } = useTranslation()
  const dialogRef = useRef<HTMLDialogElement>(null)
  // Capture the element that opened the dialog so we can restore focus on close
  // (WCAG 2.4.3). React's autoFocus fires before our showModal effect, so the
  // browser-default behaviour (return focus to opener) is unreliable in tests
  // — we keep our own reference instead.
  const triggerRef = useRef<HTMLElement | null>(
    typeof document !== 'undefined' && document.activeElement instanceof HTMLElement
      ? document.activeElement
      : null,
  )
  const { addToast } = useToastStore()
  const createGoal = useCreateGoal()

  const [title, setTitle] = useState('')
  const [area, setArea] = useState('')
  const [description, setDescription] = useState('')
  const [dueDate, setDueDate] = useState('')
  const [fieldErrors, setFieldErrors] = useState<FieldErrors>({})
  const [globalError, setGlobalError] = useState('')

  const isDirty =
    title.length > 0 ||
    area.length > 0 ||
    description.length > 0 ||
    dueDate.length > 0

  // Capture latest dirty in a ref so the dialog cancel handler always sees fresh state.
  const isDirtyRef = useRef(isDirty)
  isDirtyRef.current = isDirty

  useEffect(() => {
    const dialog = dialogRef.current
    if (!dialog) return
    dialog.showModal()
    const handleClose = () => {
      // Restore focus to the trigger (WCAG 2.4.3) before notifying parent.
      const trigger = triggerRef.current
      if (trigger && document.contains(trigger)) {
        trigger.focus()
      }
      onClose()
    }
    dialog.addEventListener('close', handleClose)
    const onCancel = (e: Event) => {
      if (!isDirtyRef.current) return
      e.preventDefault()
      if (window.confirm(t('common.errors.unsavedChanges'))) {
        dialog.close()
      }
    }
    dialog.addEventListener('cancel', onCancel)
    return () => {
      dialog.removeEventListener('close', handleClose)
      dialog.removeEventListener('cancel', onCancel)
    }
  }, [onClose, t])

  function validate(): FieldErrors {
    const errs: FieldErrors = {}
    const trimmedTitle = title.trim()
    if (!trimmedTitle) {
      errs.title = t('goals.modal.errors.titleRequired')
    } else if (trimmedTitle.length > TITLE_MAX) {
      errs.title = t('goals.modal.errors.titleTooLong')
    }
    if (area.trim().length > AREA_MAX) {
      errs.area = t('goals.modal.errors.areaTooLong')
    }
    if (description.trim().length > DESCRIPTION_MAX) {
      errs.description = t('goals.modal.errors.descriptionTooLong')
    }
    if (dueDate) {
      const parsed = new Date(dueDate)
      if (Number.isNaN(parsed.getTime())) {
        errs.dueDate = t('goals.modal.errors.dateInvalid')
      }
    }
    return errs
  }

  function attemptClose() {
    if (!isDirtyRef.current) {
      dialogRef.current?.close()
      return
    }
    if (window.confirm(t('common.errors.unsavedChanges'))) {
      dialogRef.current?.close()
    }
  }

  function mapServerError(err: unknown): string {
    if (err instanceof Error) {
      if (err.message.startsWith('429')) return t('common.errors.rateLimited')
      if (err.message.startsWith('400')) return t('common.errors.invalidInput')
    }
    return t('common.errors.saveFailed')
  }

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    const errs = validate()
    if (Object.keys(errs).length > 0) {
      setFieldErrors(errs)
      setGlobalError(t('common.errors.invalidInput'))
      return
    }
    setFieldErrors({})
    setGlobalError('')

    try {
      await createGoal.mutateAsync({
        title: title.trim(),
        area: area.trim() || undefined,
        description: description.trim() || undefined,
        due_date: dueDate ? new Date(dueDate).toISOString() : null,
      })
      addToast({ type: 'success', message: t('goals.modal.createdToast') })
      dialogRef.current?.close()
    } catch (err) {
      setGlobalError(mapServerError(err))
    }
  }

  function handleBackdropClick(e: React.MouseEvent<HTMLDialogElement>) {
    if (e.target === dialogRef.current) {
      attemptClose()
    }
  }

  const descriptionCount = description.length

  return (
    <dialog
      ref={dialogRef}
      aria-modal="true"
      aria-labelledby="goal-create-modal-title"
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
            id="goal-create-modal-title"
            className="text-section"
            style={{ color: 'var(--color-text-primary)' }}
          >
            {t('goals.modal.titleCreate')}
          </h2>
          <button
            type="button"
            onClick={attemptClose}
            aria-label="Close"
            className="flex items-center justify-center w-8 h-8 rounded-md transition-colors"
            style={{ color: 'var(--color-text-muted)' }}
            onMouseEnter={(e) => { e.currentTarget.style.background = 'var(--color-bg-hover)' }}
            onMouseLeave={(e) => { e.currentTarget.style.background = 'transparent' }}
          >
            <X size={16} aria-hidden="true" />
          </button>
        </div>

        <form
          onSubmit={(e) => { void handleSubmit(e) }}
          className="flex flex-col gap-4"
          noValidate
        >
          <FormField
            id="goal-create-title"
            label={t('goals.modal.titleLabel')}
            required
            error={fieldErrors.title}
          >
            <input
              id="goal-create-title"
              type="text"
              autoFocus
              value={title}
              onChange={(e) => {
                setTitle(e.target.value)
                if (fieldErrors.title) {
                  setFieldErrors((prev) => ({ ...prev, title: undefined }))
                }
              }}
              placeholder={t('goals.modal.titlePlaceholder')}
              maxLength={TITLE_MAX}
              aria-required="true"
              aria-describedby={fieldErrors.title ? 'goal-create-title-err' : undefined}
              className="w-full rounded-md px-3 py-2 text-body"
              style={inputBaseStyle}
              onFocus={onInputFocus}
              onBlur={onInputBlur}
            />
          </FormField>

          <FormField
            id="goal-create-area"
            label={t('goals.modal.areaLabel')}
            error={fieldErrors.area}
          >
            <input
              id="goal-create-area"
              type="text"
              value={area}
              onChange={(e) => {
                setArea(e.target.value)
                if (fieldErrors.area) {
                  setFieldErrors((prev) => ({ ...prev, area: undefined }))
                }
              }}
              placeholder={t('goals.modal.areaPlaceholder')}
              maxLength={AREA_MAX}
              aria-describedby={fieldErrors.area ? 'goal-create-area-err' : undefined}
              className="w-full rounded-md px-3 py-2 text-body"
              style={inputBaseStyle}
              onFocus={onInputFocus}
              onBlur={onInputBlur}
            />
          </FormField>

          <FormField
            id="goal-create-description"
            label={t('goals.modal.descriptionLabel')}
            hint={
              descriptionCount > 0
                ? t('common.charCount', { count: descriptionCount, max: DESCRIPTION_MAX })
                : undefined
            }
            error={fieldErrors.description}
          >
            <textarea
              id="goal-create-description"
              value={description}
              onChange={(e) => {
                setDescription(e.target.value)
                if (fieldErrors.description) {
                  setFieldErrors((prev) => ({ ...prev, description: undefined }))
                }
              }}
              placeholder={t('goals.modal.descriptionPlaceholder')}
              rows={3}
              maxLength={DESCRIPTION_MAX}
              aria-describedby={
                fieldErrors.description
                  ? 'goal-create-description-err'
                  : descriptionCount > 0
                    ? 'goal-create-description-hint'
                    : undefined
              }
              className="w-full rounded-md px-3 py-2 text-body resize-none"
              style={inputBaseStyle}
              onFocus={onInputFocus}
              onBlur={onInputBlur}
            />
          </FormField>

          <FormField
            id="goal-create-due"
            label={t('goals.modal.targetDateLabel')}
            error={fieldErrors.dueDate}
          >
            <input
              id="goal-create-due"
              type="date"
              value={dueDate}
              onChange={(e) => {
                setDueDate(e.target.value)
                if (fieldErrors.dueDate) {
                  setFieldErrors((prev) => ({ ...prev, dueDate: undefined }))
                }
              }}
              aria-describedby={fieldErrors.dueDate ? 'goal-create-due-err' : undefined}
              className="w-full rounded-md px-3 py-2 text-body"
              style={inputDateStyle}
            />
          </FormField>

          {globalError && (
            <p
              role="alert"
              className="text-body-sm"
              style={{ color: 'var(--color-error)' }}
            >
              {globalError}
            </p>
          )}

          <div className="flex gap-3 mt-2">
            <button
              type="button"
              onClick={attemptClose}
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
              {createGoal.isPending ? t('common.submitting') : t('goals.modal.submitCreate')}
            </button>
          </div>
        </form>
      </div>
    </dialog>
  )
}
