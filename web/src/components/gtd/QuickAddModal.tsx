import { useRef, useEffect, useState } from 'react'
import { X } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { useCreateTask } from '../../hooks/useTasks'
import { useToastStore } from '../../stores/toastStore'
import type { Project, CreateTaskRequest } from '../../types/api'

interface QuickAddModalProps {
  projects: Project[];
  onClose: () => void;
}

export function QuickAddModal({ projects, onClose }: QuickAddModalProps) {
  const { t } = useTranslation()
  const dialogRef = useRef<HTMLDialogElement>(null)
  const { addToast } = useToastStore()
  const createTask = useCreateTask()

  const [title, setTitle] = useState('')
  const [projectId, setProjectId] = useState('')
  const [priority, setPriority] = useState<1 | 2 | 3 | 4 | 5>(3)
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
      setFormError(t('error.fieldRequired'))
      return
    }
    setFormError('')

    const payload: CreateTaskRequest = {
      title: title.trim(),
      project_id: projectId || null,
      priority,
      due_date: dueDate ? new Date(dueDate).toISOString() : null,
    }

    try {
      await createTask.mutateAsync(payload)
      addToast({ type: 'success', message: t('gtd.taskCreated') })
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

  const priorityLevels: (1 | 2 | 3 | 4 | 5)[] = [1, 2, 3, 4, 5]

  return (
    <dialog
      ref={dialogRef}
      aria-modal="true"
      aria-labelledby="modal-title"
      onClick={handleBackdropClick}
      style={{ background: 'transparent' }}
    >
      <div
        className="rounded-t-xl sm:rounded-xl w-full p-6"
        style={{
          background: 'var(--color-bg-card)',
          border: '1px solid var(--color-border)',
          minWidth: 'min(400px, 90vw)',
          maxWidth: '448px',
        }}
      >
        <div className="flex items-center justify-between mb-5">
          <h2
            id="modal-title"
            className="text-section"
            style={{ color: 'var(--color-text-primary)' }}
          >
            {t('modal.addTask.title')}
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
          {/* Title */}
          <div>
            <label
              htmlFor="task-title"
              className="text-label block mb-1"
              style={{ color: 'var(--color-text-muted)' }}
            >
              {t('modal.addTask.titleLabel')}
            </label>
            <input
              id="task-title"
              type="text"
              autoFocus
              value={title}
              onChange={(e) => setTitle(e.target.value)}
              placeholder={t('modal.addTask.titlePlaceholder')}
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

          {/* Project */}
          <div>
            <label
              htmlFor="task-project"
              className="text-label block mb-1"
              style={{ color: 'var(--color-text-muted)' }}
            >
              {t('modal.addTask.projectLabel')}
            </label>
            <select
              id="task-project"
              value={projectId}
              onChange={(e) => setProjectId(e.target.value)}
              className="w-full rounded-md px-3 py-2 text-body"
              style={{
                background: 'var(--color-bg-input)',
                border: '1px solid var(--color-border)',
                color: 'var(--color-text-primary)',
              }}
            >
              <option value="">—</option>
              {projects.map((p) => (
                <option key={p.id} value={p.id}>{p.title}</option>
              ))}
            </select>
          </div>

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

          {/* Due date */}
          <div>
            <label
              htmlFor="task-due"
              className="text-label block mb-1"
              style={{ color: 'var(--color-text-muted)' }}
            >
              {t('modal.addTask.dueDateLabel')}
            </label>
            <input
              id="task-due"
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
              disabled={createTask.isPending}
              className="flex-1 py-2 rounded-md text-body font-medium transition-colors"
              style={{
                background: 'var(--color-accent-blue)',
                color: 'var(--color-bg-base)',
                border: 'none',
                cursor: createTask.isPending ? 'not-allowed' : 'pointer',
                opacity: createTask.isPending ? 0.7 : 1,
              }}
            >
              {createTask.isPending ? '...' : t('modal.addTask.submit')}
            </button>
          </div>
        </form>
      </div>
    </dialog>
  )
}
