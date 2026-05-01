import { useRef, useEffect, useState } from 'react'
import { X } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { useCreateProject } from '../../hooks/useProjects'
import { useToastStore } from '../../stores/toastStore'
import type { Goal } from '../../types/api'

interface CreateProjectModalProps {
  goals: Goal[];
  onClose: () => void;
}

export function CreateProjectModal({ goals, onClose }: CreateProjectModalProps) {
  const { t } = useTranslation()
  const dialogRef = useRef<HTMLDialogElement>(null)
  const { addToast } = useToastStore()
  const createProject = useCreateProject()

  const [name, setName] = useState('')
  const [title, setTitle] = useState('')
  const [area, setArea] = useState('')
  const [description, setDescription] = useState('')
  const [goalId, setGoalId] = useState('')
  const [priority, setPriority] = useState<1 | 2 | 3 | 4 | 5>(3)
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
    if (!name.trim() || !title.trim()) {
      setFormError('Name and title are required')
      return
    }
    setFormError('')

    try {
      await createProject.mutateAsync({
        name: name.trim(),
        title: title.trim(),
        area: area.trim() || undefined,
        description: description.trim() || undefined,
        goal_id: goalId || null,
        priority,
      })
      addToast({ type: 'success', message: 'Project created!' })
      dialogRef.current?.close()
    } catch (err) {
      const message = err instanceof Error && err.message.includes('409')
        ? 'Project name already exists'
        : t('error.loadFailed')
      setFormError(message)
    }
  }

  const handleBackdropClick = (e: React.MouseEvent<HTMLDialogElement>) => {
    if (e.target === dialogRef.current) {
      dialogRef.current?.close()
    }
  }

  const priorityLevels: (1 | 2 | 3 | 4 | 5)[] = [1, 2, 3, 4, 5]

  return (
    <dialog
      ref={dialogRef}
      aria-modal="true"
      aria-labelledby="project-modal-title"
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
            id="project-modal-title"
            className="text-section"
            style={{ color: 'var(--color-text-primary)' }}
          >
            {t('gtd.addProject')}
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
              htmlFor="project-title"
              className="text-label block mb-1"
              style={{ color: 'var(--color-text-muted)' }}
            >
              Title
            </label>
            <input
              id="project-title"
              type="text"
              autoFocus
              value={title}
              onChange={(e) => setTitle(e.target.value)}
              placeholder="Project title..."
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
              htmlFor="project-name"
              className="text-label block mb-1"
              style={{ color: 'var(--color-text-muted)' }}
            >
              Repo / slug name
            </label>
            <input
              id="project-name"
              type="text"
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="kebab-case identifier..."
              className="w-full rounded-md px-3 py-2 text-body font-mono"
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
              htmlFor="project-area"
              className="text-label block mb-1"
              style={{ color: 'var(--color-text-muted)' }}
            >
              Area (optional)
            </label>
            <input
              id="project-area"
              type="text"
              value={area}
              onChange={(e) => setArea(e.target.value)}
              placeholder="e.g. Engineering, Product..."
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
              htmlFor="project-description"
              className="text-label block mb-1"
              style={{ color: 'var(--color-text-muted)' }}
            >
              Description (optional)
            </label>
            <textarea
              id="project-description"
              value={description}
              onChange={(e) => setDescription(e.target.value)}
              placeholder="What is this project about?"
              rows={2}
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

          {/* Goal */}
          {goals.length > 0 && (
            <div>
              <label
                htmlFor="project-goal"
                className="text-label block mb-1"
                style={{ color: 'var(--color-text-muted)' }}
              >
                Linked goal (optional)
              </label>
              <select
                id="project-goal"
                value={goalId}
                onChange={(e) => setGoalId(e.target.value)}
                className="w-full rounded-md px-3 py-2 text-body"
                style={{
                  background: 'var(--color-bg-input)',
                  border: '1px solid var(--color-border)',
                  color: 'var(--color-text-primary)',
                }}
              >
                <option value="">—</option>
                {goals.map((g) => (
                  <option key={g.id} value={g.id}>{g.title}</option>
                ))}
              </select>
            </div>
          )}

          {/* Priority */}
          <div>
            <span
              className="text-label block mb-1"
              style={{ color: 'var(--color-text-muted)' }}
            >
              {t('modal.addTask.priorityLabel')}
            </span>
            <div className="flex gap-2">
              {priorityLevels.map((lvl) => (
                <button
                  key={lvl}
                  type="button"
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
              {createProject.isPending ? '...' : t('gtd.addProject')}
            </button>
          </div>
        </form>
      </div>
    </dialog>
  )
}
