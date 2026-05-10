import { useEffect, useMemo, useRef, useState } from 'react'
import { X } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { useCreateProject } from '../../hooks/useProjects'
import { useToastStore } from '../../stores/toastStore'
import type { Goal } from '../../types/api'
import { FormField } from '../ui/FormField'
import { inputBaseStyle, onInputBlur, onInputFocus } from '../ui/formStyles'

interface ProjectCreateModalProps {
  goals: Goal[];
  onClose: () => void;
}

interface FieldErrors {
  title?: string;
  name?: string;
  area?: string;
  description?: string;
}

const TITLE_MAX = 200
const NAME_MAX = 64
const AREA_MAX = 80
const DESCRIPTION_MAX = 2000
const SLUG_RE = /^[a-z0-9]+(-[a-z0-9]+)*$/

function slugify(input: string): string {
  return input
    .toLowerCase()
    .normalize('NFD')
    // Strip combining diacritical marks (U+0300–U+036F).
    .replace(/[̀-ͯ]/g, '')
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '')
    .slice(0, NAME_MAX)
}

export function ProjectCreateModal({ goals, onClose }: ProjectCreateModalProps) {
  const { t } = useTranslation()
  const dialogRef = useRef<HTMLDialogElement>(null)
  // See GoalCreateModal — capture trigger so we can restore focus on close.
  const triggerRef = useRef<HTMLElement | null>(
    typeof document !== 'undefined' && document.activeElement instanceof HTMLElement
      ? document.activeElement
      : null,
  )
  const { addToast } = useToastStore()
  const createProject = useCreateProject()

  const [title, setTitle] = useState('')
  const [name, setName] = useState('')
  const [nameTouched, setNameTouched] = useState(false)
  const [area, setArea] = useState('')
  const [description, setDescription] = useState('')
  const [goalId, setGoalId] = useState('')
  const [priority, setPriority] = useState<1 | 2 | 3 | 4 | 5>(3)
  const [fieldErrors, setFieldErrors] = useState<FieldErrors>({})
  const [globalError, setGlobalError] = useState('')

  const isDirty =
    title.length > 0 ||
    name.length > 0 ||
    area.length > 0 ||
    description.length > 0 ||
    goalId.length > 0 ||
    priority !== 3

  const isDirtyRef = useRef(isDirty)
  isDirtyRef.current = isDirty

  const suggestedSlug = useMemo(() => slugify(title), [title])
  const showSuggestion =
    !nameTouched && name === '' && suggestedSlug.length > 0 && suggestedSlug !== name

  useEffect(() => {
    const dialog = dialogRef.current
    if (!dialog) return
    dialog.showModal()
    const handleClose = () => {
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
      errs.title = t('projects.modal.errors.titleRequired')
    } else if (trimmedTitle.length > TITLE_MAX) {
      errs.title = t('projects.modal.errors.titleTooLong')
    }
    const trimmedName = name.trim()
    if (!trimmedName) {
      errs.name = t('projects.modal.errors.nameRequired')
    } else if (trimmedName.length > NAME_MAX) {
      errs.name = t('projects.modal.errors.nameTooLong')
    } else if (!SLUG_RE.test(trimmedName)) {
      errs.name = t('projects.modal.errors.nameSlugInvalid')
    }
    if (area.trim().length > AREA_MAX) {
      errs.area = t('projects.modal.errors.areaTooLong')
    }
    if (description.trim().length > DESCRIPTION_MAX) {
      errs.description = t('projects.modal.errors.descriptionTooLong')
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
      if (err.message.startsWith('409')) {
        return t('projects.modal.errors.nameExists')
      }
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
      await createProject.mutateAsync({
        name: name.trim(),
        title: title.trim(),
        area: area.trim() || undefined,
        description: description.trim() || undefined,
        goal_id: goalId || null,
        priority,
      })
      addToast({ type: 'success', message: t('projects.modal.createdToast') })
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

  const priorityLevels: (1 | 2 | 3 | 4 | 5)[] = [1, 2, 3, 4, 5]

  return (
    <dialog
      ref={dialogRef}
      aria-modal="true"
      aria-labelledby="project-create-modal-title"
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
            id="project-create-modal-title"
            className="text-section"
            style={{ color: 'var(--color-text-primary)' }}
          >
            {t('projects.modal.titleCreate')}
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
            id="project-create-title"
            label={t('projects.modal.titleLabel')}
            required
            error={fieldErrors.title}
          >
            <input
              id="project-create-title"
              type="text"
              autoFocus
              value={title}
              onChange={(e) => {
                setTitle(e.target.value)
                if (fieldErrors.title) {
                  setFieldErrors((prev) => ({ ...prev, title: undefined }))
                }
              }}
              placeholder={t('projects.modal.titlePlaceholder')}
              maxLength={TITLE_MAX}
              aria-required="true"
              aria-describedby={fieldErrors.title ? 'project-create-title-err' : undefined}
              className="w-full rounded-md px-3 py-2 text-body"
              style={inputBaseStyle}
              onFocus={onInputFocus}
              onBlur={onInputBlur}
            />
          </FormField>

          <FormField
            id="project-create-name"
            label={t('projects.modal.nameLabel')}
            required
            hint={t('projects.modal.nameHint')}
            error={fieldErrors.name}
          >
            <input
              id="project-create-name"
              type="text"
              value={name}
              onChange={(e) => {
                setName(e.target.value)
                setNameTouched(true)
                if (fieldErrors.name) {
                  setFieldErrors((prev) => ({ ...prev, name: undefined }))
                }
              }}
              placeholder={t('projects.modal.namePlaceholder')}
              maxLength={NAME_MAX}
              aria-required="true"
              aria-describedby={
                fieldErrors.name ? 'project-create-name-err' : 'project-create-name-hint'
              }
              className="w-full rounded-md px-3 py-2 text-body font-mono"
              style={inputBaseStyle}
              onFocus={onInputFocus}
              onBlur={onInputBlur}
            />
            {showSuggestion && (
              <button
                type="button"
                onClick={() => {
                  setName(suggestedSlug)
                  setNameTouched(true)
                }}
                className="text-caption mt-1 underline"
                style={{ color: 'var(--color-accent-blue)', background: 'transparent', border: 'none', cursor: 'pointer', padding: 0 }}
              >
                {t('projects.modal.nameSuggestion', { slug: suggestedSlug })}
              </button>
            )}
          </FormField>

          <FormField
            id="project-create-area"
            label={t('projects.modal.areaLabel')}
            error={fieldErrors.area}
          >
            <input
              id="project-create-area"
              type="text"
              value={area}
              onChange={(e) => {
                setArea(e.target.value)
                if (fieldErrors.area) {
                  setFieldErrors((prev) => ({ ...prev, area: undefined }))
                }
              }}
              placeholder={t('projects.modal.areaPlaceholder')}
              maxLength={AREA_MAX}
              aria-describedby={fieldErrors.area ? 'project-create-area-err' : undefined}
              className="w-full rounded-md px-3 py-2 text-body"
              style={inputBaseStyle}
              onFocus={onInputFocus}
              onBlur={onInputBlur}
            />
          </FormField>

          <FormField
            id="project-create-description"
            label={t('projects.modal.descriptionLabel')}
            error={fieldErrors.description}
          >
            <textarea
              id="project-create-description"
              value={description}
              onChange={(e) => {
                setDescription(e.target.value)
                if (fieldErrors.description) {
                  setFieldErrors((prev) => ({ ...prev, description: undefined }))
                }
              }}
              placeholder={t('projects.modal.descriptionPlaceholder')}
              rows={2}
              maxLength={DESCRIPTION_MAX}
              aria-describedby={
                fieldErrors.description ? 'project-create-description-err' : undefined
              }
              className="w-full rounded-md px-3 py-2 text-body resize-none"
              style={inputBaseStyle}
              onFocus={onInputFocus}
              onBlur={onInputBlur}
            />
          </FormField>

          {goals.length > 0 && (
            <FormField id="project-create-goal" label={t('projects.modal.goalLabel')}>
              <select
                id="project-create-goal"
                value={goalId}
                onChange={(e) => setGoalId(e.target.value)}
                className="w-full rounded-md px-3 py-2 text-body"
                style={{
                  background: 'var(--color-bg-input)',
                  border: '1px solid var(--color-border)',
                  color: 'var(--color-text-primary)',
                }}
              >
                <option value="">{t('projects.modal.goalPlaceholderNone')}</option>
                {goals.map((g) => (
                  <option key={g.id} value={g.id}>{g.title}</option>
                ))}
              </select>
            </FormField>
          )}

          <div>
            <span
              id="project-create-priority-label"
              className="text-label block mb-1"
              style={{ color: 'var(--color-text-muted)' }}
            >
              {t('projects.modal.priorityLabel')}
            </span>
            <div
              className="flex gap-2"
              role="radiogroup"
              aria-labelledby="project-create-priority-label"
            >
              {priorityLevels.map((lvl) => (
                <button
                  key={lvl}
                  type="button"
                  role="radio"
                  aria-checked={priority === lvl}
                  onClick={() => setPriority(lvl)}
                  className="flex-1 py-1.5 rounded-md text-caption font-mono transition-colors"
                  style={{
                    border: `1px solid ${priority === lvl ? 'var(--color-accent-blue)' : 'var(--color-border)'}`,
                    background: priority === lvl ? 'var(--color-accent-blue)' : 'var(--color-bg-input)',
                    color: priority === lvl ? 'var(--color-bg-base)' : 'var(--color-text-muted)',
                  }}
                >
                  {lvl}
                </button>
              ))}
            </div>
          </div>

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
              disabled={createProject.isPending}
              className="flex-1 py-2 rounded-md text-body font-medium transition-colors"
              style={{
                background: 'var(--color-accent-blue)',
                color: 'var(--color-bg-base)',
                border: 'none',
                cursor: createProject.isPending ? 'not-allowed' : 'pointer',
                opacity: createProject.isPending ? 0.7 : 1,
              }}
            >
              {createProject.isPending
                ? t('common.submitting')
                : t('projects.modal.submitCreate')}
            </button>
          </div>
        </form>
      </div>
    </dialog>
  )
}
