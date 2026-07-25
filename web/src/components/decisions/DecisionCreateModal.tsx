import { useEffect, useRef, useState } from 'react'
import { X } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { useLogDecision } from '../../hooks/useDecisions'
import { useToastStore } from '../../stores/toastStore'
import type { Project } from '../../types/api'
import { FormField } from '../ui/FormField'
import { inputBaseStyle, onInputBlur, onInputFocus } from '../ui/formStyles'

interface DecisionCreateModalProps {
  projects: Project[];
  /** Optional pre-selected project (e.g. from ProjectDetailPage) */
  initialProjectId?: string;
  onClose: () => void;
}

interface FieldErrors {
  title?: string;
  context?: string;
  decision?: string;
  rationale?: string;
  alternatives?: string;
  repoName?: string;
}

const TITLE_MAX = 200
const TEXT_MAX = 4000
const REPO_MAX = 80

export function DecisionCreateModal({
  projects,
  initialProjectId,
  onClose,
}: DecisionCreateModalProps) {
  const { t } = useTranslation()
  const dialogRef = useRef<HTMLDialogElement>(null)
  // See GoalModal — capture trigger so we can restore focus on close.
  const triggerRef = useRef<HTMLElement | null>(
    typeof document !== 'undefined' && document.activeElement instanceof HTMLElement
      ? document.activeElement
      : null,
  )
  const { addToast } = useToastStore()
  const logDecision = useLogDecision()

  const [title, setTitle] = useState('')
  const [context, setContext] = useState('')
  const [decision, setDecision] = useState('')
  const [rationale, setRationale] = useState('')
  const [alternatives, setAlternatives] = useState('')
  const [repoName, setRepoName] = useState('')
  const [projectId, setProjectId] = useState(initialProjectId ?? '')
  const [fieldErrors, setFieldErrors] = useState<FieldErrors>({})
  const [globalError, setGlobalError] = useState('')

  const isDirty =
    title.length > 0 ||
    context.length > 0 ||
    decision.length > 0 ||
    rationale.length > 0 ||
    alternatives.length > 0 ||
    repoName.length > 0 ||
    projectId !== (initialProjectId ?? '')

  const isDirtyRef = useRef(isDirty)
  isDirtyRef.current = isDirty

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
    const tt = title.trim()
    if (!tt) errs.title = t('decisions.modal.errors.titleRequired')
    else if (tt.length > TITLE_MAX) errs.title = t('decisions.modal.errors.titleTooLong')

    const ctx = context.trim()
    if (!ctx) errs.context = t('decisions.modal.errors.contextRequired')
    else if (ctx.length > TEXT_MAX) errs.context = t('decisions.modal.errors.contextTooLong')

    const dec = decision.trim()
    if (!dec) errs.decision = t('decisions.modal.errors.decisionRequired')
    else if (dec.length > TEXT_MAX) errs.decision = t('decisions.modal.errors.decisionTooLong')

    const rat = rationale.trim()
    if (!rat) errs.rationale = t('decisions.modal.errors.rationaleRequired')
    else if (rat.length > TEXT_MAX) errs.rationale = t('decisions.modal.errors.rationaleTooLong')

    if (alternatives.trim().length > TEXT_MAX) {
      errs.alternatives = t('decisions.modal.errors.alternativesTooLong')
    }
    if (repoName.trim().length > REPO_MAX) {
      errs.repoName = t('decisions.modal.errors.repoTooLong')
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
      await logDecision.mutateAsync({
        title: title.trim(),
        context: context.trim(),
        decision: decision.trim(),
        rationale: rationale.trim(),
        alternatives: alternatives.trim() || undefined,
        repo_name: repoName.trim() || undefined,
        project_id: projectId || null,
      })
      addToast({ type: 'success', message: t('decisions.modal.createdToast') })
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

  return (
    <dialog
      ref={dialogRef}
      aria-modal="true"
      aria-labelledby="decision-create-modal-title"
      onClick={handleBackdropClick}
      style={{ background: 'transparent' }}
    >
      <div
        className="rounded-xl w-full p-6"
        style={{
          background: 'var(--color-bg-card)',
          border: '1px solid var(--color-border)',
          minWidth: 'min(400px, 90vw)',
          maxWidth: '560px',
        }}
      >
        <div className="flex items-center justify-between mb-5">
          <h2
            id="decision-create-modal-title"
            className="text-section"
            style={{ color: 'var(--color-text-primary)' }}
          >
            {t('decisions.modal.titleCreate')}
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
            id="decision-create-title"
            label={t('decisions.modal.titleLabel')}
            required
            error={fieldErrors.title}
          >
            <input
              id="decision-create-title"
              type="text"
              autoFocus
              value={title}
              onChange={(e) => {
                setTitle(e.target.value)
                if (fieldErrors.title) {
                  setFieldErrors((prev) => ({ ...prev, title: undefined }))
                }
              }}
              placeholder={t('decisions.modal.titlePlaceholder')}
              maxLength={TITLE_MAX}
              aria-required="true"
              aria-describedby={fieldErrors.title ? 'decision-create-title-err' : undefined}
              className="w-full rounded-md px-3 py-2 text-body"
              style={inputBaseStyle}
              onFocus={onInputFocus}
              onBlur={onInputBlur}
            />
          </FormField>

          <FormField
            id="decision-create-context"
            label={t('decisions.modal.contextLabel')}
            required
            hint={
              context.length > 0
                ? t('common.charCount', { count: context.length, max: TEXT_MAX })
                : undefined
            }
            error={fieldErrors.context}
          >
            <textarea
              id="decision-create-context"
              value={context}
              onChange={(e) => {
                setContext(e.target.value)
                if (fieldErrors.context) {
                  setFieldErrors((prev) => ({ ...prev, context: undefined }))
                }
              }}
              placeholder={t('decisions.modal.contextPlaceholder')}
              rows={3}
              maxLength={TEXT_MAX}
              aria-required="true"
              aria-describedby={
                fieldErrors.context
                  ? 'decision-create-context-err'
                  : context.length > 0
                    ? 'decision-create-context-hint'
                    : undefined
              }
              className="w-full rounded-md px-3 py-2 text-body resize-none"
              style={inputBaseStyle}
              onFocus={onInputFocus}
              onBlur={onInputBlur}
            />
          </FormField>

          <FormField
            id="decision-create-decision"
            label={t('decisions.modal.decisionLabel')}
            required
            error={fieldErrors.decision}
          >
            <textarea
              id="decision-create-decision"
              value={decision}
              onChange={(e) => {
                setDecision(e.target.value)
                if (fieldErrors.decision) {
                  setFieldErrors((prev) => ({ ...prev, decision: undefined }))
                }
              }}
              placeholder={t('decisions.modal.decisionPlaceholder')}
              rows={3}
              maxLength={TEXT_MAX}
              aria-required="true"
              aria-describedby={fieldErrors.decision ? 'decision-create-decision-err' : undefined}
              className="w-full rounded-md px-3 py-2 text-body resize-none"
              style={inputBaseStyle}
              onFocus={onInputFocus}
              onBlur={onInputBlur}
            />
          </FormField>

          <FormField
            id="decision-create-rationale"
            label={t('decisions.modal.rationaleLabel')}
            required
            error={fieldErrors.rationale}
          >
            <textarea
              id="decision-create-rationale"
              value={rationale}
              onChange={(e) => {
                setRationale(e.target.value)
                if (fieldErrors.rationale) {
                  setFieldErrors((prev) => ({ ...prev, rationale: undefined }))
                }
              }}
              placeholder={t('decisions.modal.rationalePlaceholder')}
              rows={3}
              maxLength={TEXT_MAX}
              aria-required="true"
              aria-describedby={fieldErrors.rationale ? 'decision-create-rationale-err' : undefined}
              className="w-full rounded-md px-3 py-2 text-body resize-none"
              style={inputBaseStyle}
              onFocus={onInputFocus}
              onBlur={onInputBlur}
            />
          </FormField>

          <FormField
            id="decision-create-alternatives"
            label={t('decisions.modal.alternativesLabel')}
            error={fieldErrors.alternatives}
          >
            <textarea
              id="decision-create-alternatives"
              value={alternatives}
              onChange={(e) => {
                setAlternatives(e.target.value)
                if (fieldErrors.alternatives) {
                  setFieldErrors((prev) => ({ ...prev, alternatives: undefined }))
                }
              }}
              placeholder={t('decisions.modal.alternativesPlaceholder')}
              rows={2}
              maxLength={TEXT_MAX}
              aria-describedby={
                fieldErrors.alternatives ? 'decision-create-alternatives-err' : undefined
              }
              className="w-full rounded-md px-3 py-2 text-body resize-none"
              style={inputBaseStyle}
              onFocus={onInputFocus}
              onBlur={onInputBlur}
            />
          </FormField>

          <div className="flex flex-col sm:flex-row gap-4">
            <div className="flex-1">
              <FormField
                id="decision-create-repo"
                label={t('decisions.modal.repoLabel')}
                error={fieldErrors.repoName}
              >
                <input
                  id="decision-create-repo"
                  type="text"
                  value={repoName}
                  onChange={(e) => {
                    setRepoName(e.target.value)
                    if (fieldErrors.repoName) {
                      setFieldErrors((prev) => ({ ...prev, repoName: undefined }))
                    }
                  }}
                  placeholder={t('decisions.modal.repoPlaceholder')}
                  maxLength={REPO_MAX}
                  aria-describedby={fieldErrors.repoName ? 'decision-create-repo-err' : undefined}
                  className="w-full rounded-md px-3 py-2 text-body"
                  style={inputBaseStyle}
                  onFocus={onInputFocus}
                  onBlur={onInputBlur}
                />
              </FormField>
            </div>
            <div className="flex-1">
              <FormField
                id="decision-create-project"
                label={t('decisions.modal.projectLabel')}
              >
                <select
                  id="decision-create-project"
                  value={projectId}
                  onChange={(e) => setProjectId(e.target.value)}
                  className="w-full rounded-md px-3 py-2 text-body"
                  style={{
                    background: 'var(--color-bg-input)',
                    border: '1px solid var(--color-border)',
                    color: 'var(--color-text-primary)',
                  }}
                >
                  <option value="">{t('decisions.modal.projectPlaceholderNone')}</option>
                  {projects.map((p) => (
                    <option key={p.id} value={p.id}>{p.title}</option>
                  ))}
                </select>
              </FormField>
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
              disabled={logDecision.isPending}
              className="flex-1 py-2 rounded-md text-body font-medium transition-colors"
              style={{
                background: 'var(--color-accent-blue)',
                color: 'var(--color-bg-base)',
                border: 'none',
                cursor: logDecision.isPending ? 'not-allowed' : 'pointer',
                opacity: logDecision.isPending ? 0.7 : 1,
              }}
            >
              {logDecision.isPending
                ? t('common.submitting')
                : t('decisions.modal.submit')}
            </button>
          </div>
        </form>
      </div>
    </dialog>
  )
}
