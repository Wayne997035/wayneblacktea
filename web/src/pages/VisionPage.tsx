import { useState, useRef, useEffect } from 'react'
import { Eye, Plus, X, ChevronDown } from 'lucide-react'
import { useVision, useCreateVision, useUpdateVision } from '../hooks/useVision'
import { useToastStore } from '../stores/toastStore'
import { LoadingSkeleton } from '../components/ui/LoadingSkeleton'
import { EmptyState } from '../components/ui/EmptyState'
import type { VisionItem, VisionStatus } from '../types/api'

type StatusTab = 'all' | VisionStatus

const STATUS_TABS: StatusTab[] = ['all', 'open', 'promoted', 'archived']

const STATUS_LABELS: Record<StatusTab, string> = {
  all: 'All',
  open: 'Open',
  promoted: 'Promoted',
  archived: 'Archived',
}

const STATUS_STYLE: Record<VisionStatus, { bg: string; text: string }> = {
  open:     { bg: 'var(--color-status-active-bg)',    text: 'var(--color-status-active-text)' },
  promoted: { bg: 'var(--color-status-completed-bg)', text: 'var(--color-status-completed-text)' },
  archived: { bg: 'var(--color-status-archived-bg)',  text: 'var(--color-status-archived-text)' },
}

interface VisionCardProps {
  item: VisionItem
}

function VisionCard({ item }: VisionCardProps) {
  const updateVision = useUpdateVision()
  const { addToast } = useToastStore()
  const [menuOpen, setMenuOpen] = useState(false)
  const menuRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (!menuOpen) return
    function handleClickOutside(e: MouseEvent) {
      if (menuRef.current && !menuRef.current.contains(e.target as Node)) {
        setMenuOpen(false)
      }
    }
    document.addEventListener('mousedown', handleClickOutside)
    return () => document.removeEventListener('mousedown', handleClickOutside)
  }, [menuOpen])

  function handleStatusChange(status: VisionStatus) {
    setMenuOpen(false)
    updateVision.mutate(
      { id: item.id, status },
      {
        onSuccess: () => {
          addToast({ type: 'success', message: `Vision item marked as ${status}` })
        },
        onError: () => {
          addToast({ type: 'error', message: 'Could not update vision item. Try again.' })
        },
      }
    )
  }

  const { bg, text } = STATUS_STYLE[item.status]

  return (
    <article
      className="rounded-lg p-4"
      style={{
        background: 'var(--color-bg-card)',
        border: '1px solid var(--color-border)',
      }}
    >
      <div className="flex items-start justify-between gap-3">
        <div className="flex-1 min-w-0">
          <div className="flex items-center gap-2 mb-1 flex-wrap">
            <span
              className="px-2 py-0.5 rounded-full font-mono whitespace-nowrap uppercase"
              style={{ background: bg, color: text, fontSize: '10px' }}
            >
              {item.status}
            </span>
            <span className="text-caption shrink-0" style={{ color: 'var(--color-text-muted)' }}>
              {new Date(item.created_at).toLocaleDateString(undefined, {
                month: 'short',
                day: 'numeric',
                year: 'numeric',
              })}
            </span>
          </div>
          <p className="text-body font-medium" style={{ color: 'var(--color-text-primary)' }}>
            {item.title}
          </p>
          {item.description && (
            <p
              className="text-body-sm mt-1"
              style={{
                color: 'var(--color-text-muted)',
                display: '-webkit-box',
                WebkitLineClamp: 3,
                WebkitBoxOrient: 'vertical',
                overflow: 'hidden',
              }}
            >
              {item.description}
            </p>
          )}
        </div>

        <div className="relative shrink-0" ref={menuRef}>
          <button
            type="button"
            aria-label="Change status"
            aria-haspopup="menu"
            aria-expanded={menuOpen}
            onClick={() => setMenuOpen((v) => !v)}
            disabled={updateVision.isPending}
            className="flex items-center gap-1 px-2 py-1 rounded-md text-body-sm transition-colors"
            style={{
              background: 'var(--color-bg-input)',
              border: '1px solid var(--color-border)',
              color: 'var(--color-text-muted)',
              cursor: updateVision.isPending ? 'not-allowed' : 'pointer',
              opacity: updateVision.isPending ? 0.6 : 1,
            }}
          >
            <ChevronDown size={12} aria-hidden="true" />
          </button>

          {menuOpen && (
            <div
              role="menu"
              className="absolute right-0 top-full mt-1 z-10 rounded-md py-1 min-w-[120px]"
              style={{
                background: 'var(--color-bg-card)',
                border: '1px solid var(--color-border)',
                boxShadow: '0 4px 12px rgba(0,0,0,0.3)',
              }}
            >
              {(['open', 'promoted', 'archived'] as VisionStatus[]).map((s) => (
                <button
                  key={s}
                  type="button"
                  role="menuitem"
                  onClick={() => handleStatusChange(s)}
                  className="w-full text-left px-3 py-1.5 text-body-sm capitalize transition-colors"
                  style={{
                    background: item.status === s ? 'var(--color-bg-input)' : 'transparent',
                    color: item.status === s ? 'var(--color-text-primary)' : 'var(--color-text-muted)',
                    border: 'none',
                    cursor: 'pointer',
                  }}
                >
                  {s}
                </button>
              ))}
            </div>
          )}
        </div>
      </div>
    </article>
  )
}

interface CreateVisionFormProps {
  onClose: () => void
}

function CreateVisionForm({ onClose }: CreateVisionFormProps) {
  const createVision = useCreateVision()
  const { addToast } = useToastStore()
  const [title, setTitle] = useState('')
  const [description, setDescription] = useState('')
  const [titleError, setTitleError] = useState('')
  const titleRef = useRef<HTMLInputElement>(null)

  useEffect(() => {
    titleRef.current?.focus()
  }, [])

  function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    if (!title.trim()) {
      setTitleError('Title is required')
      return
    }
    createVision.mutate(
      {
        title: title.trim(),
        description: description.trim() || null,
        status: 'open',
      },
      {
        onSuccess: () => {
          addToast({ type: 'success', message: 'Vision item created' })
          onClose()
        },
        onError: () => {
          addToast({ type: 'error', message: 'Could not create vision item. Try again.' })
        },
      }
    )
  }

  const inputStyle: React.CSSProperties = {
    background: 'var(--color-bg-input)',
    border: '1px solid var(--color-border)',
    color: 'var(--color-text-primary)',
  }

  return (
    <div
      className="rounded-lg p-4 mb-4"
      style={{
        background: 'var(--color-bg-card)',
        border: '1px solid var(--color-border)',
      }}
    >
      <div className="flex items-center justify-between mb-3">
        <p className="text-body font-medium" style={{ color: 'var(--color-text-primary)' }}>
          New Vision Item
        </p>
        <button
          type="button"
          aria-label="Close form"
          onClick={onClose}
          className="rounded-md p-1 transition-colors"
          style={{ background: 'transparent', border: 'none', color: 'var(--color-text-muted)', cursor: 'pointer' }}
        >
          <X size={14} aria-hidden="true" />
        </button>
      </div>

      <form onSubmit={handleSubmit} noValidate>
        <div className="mb-3">
          <label htmlFor="vision-title" className="block text-body-sm mb-1" style={{ color: 'var(--color-text-muted)' }}>
            Title <span aria-hidden="true">*</span>
          </label>
          <input
            id="vision-title"
            ref={titleRef}
            type="text"
            value={title}
            onChange={(e) => {
              setTitle(e.target.value)
              if (titleError) setTitleError('')
            }}
            placeholder="What do you want to achieve?"
            className="w-full rounded-md px-3 py-2 text-body"
            style={inputStyle}
            aria-describedby={titleError ? 'vision-title-error' : undefined}
            aria-invalid={Boolean(titleError)}
          />
          {titleError && (
            <p id="vision-title-error" className="text-body-sm mt-1" role="alert" style={{ color: 'var(--color-error)' }}>
              {titleError}
            </p>
          )}
        </div>

        <div className="mb-4">
          <label htmlFor="vision-description" className="block text-body-sm mb-1" style={{ color: 'var(--color-text-muted)' }}>
            Description
          </label>
          <textarea
            id="vision-description"
            value={description}
            onChange={(e) => setDescription(e.target.value)}
            placeholder="Optional details…"
            rows={3}
            className="w-full rounded-md px-3 py-2 text-body resize-none"
            style={inputStyle}
          />
        </div>

        <div className="flex items-center gap-2">
          <button
            type="submit"
            disabled={createVision.isPending}
            className="flex items-center gap-2 px-4 py-2 rounded-md text-body-sm transition-colors"
            style={{
              background: 'var(--color-accent-blue)',
              color: 'var(--color-bg-base)',
              border: 'none',
              cursor: createVision.isPending ? 'not-allowed' : 'pointer',
              opacity: createVision.isPending ? 0.6 : 1,
            }}
          >
            {createVision.isPending ? 'Saving…' : 'Add Vision'}
          </button>
          <button
            type="button"
            onClick={onClose}
            className="px-4 py-2 rounded-md text-body-sm transition-colors"
            style={{
              background: 'var(--color-bg-input)',
              color: 'var(--color-text-muted)',
              border: '1px solid var(--color-border)',
              cursor: 'pointer',
            }}
          >
            Cancel
          </button>
        </div>
      </form>
    </div>
  )
}

export function VisionPage() {
  const { data: items, isLoading, isError } = useVision()
  const [activeTab, setActiveTab] = useState<StatusTab>('open')
  const [creating, setCreating] = useState(false)
  const tabRefs = useRef<(HTMLButtonElement | null)[]>([])

  const filtered = (items ?? []).filter((item) =>
    activeTab === 'all' ? true : item.status === activeTab
  )

  function handleTabKeyDown(e: React.KeyboardEvent, idx: number) {
    if (e.key === 'ArrowRight') {
      e.preventDefault()
      const next = (idx + 1) % STATUS_TABS.length
      setActiveTab(STATUS_TABS[next])
      tabRefs.current[next]?.focus()
    } else if (e.key === 'ArrowLeft') {
      e.preventDefault()
      const prev = (idx - 1 + STATUS_TABS.length) % STATUS_TABS.length
      setActiveTab(STATUS_TABS[prev])
      tabRefs.current[prev]?.focus()
    }
  }

  return (
    <div className="p-6 max-w-[900px] mx-auto">
      <div className="flex items-center justify-between mb-4">
        <div className="flex items-center gap-2">
          <Eye size={20} style={{ color: 'var(--color-accent-blue)' }} aria-hidden="true" />
          <h1 className="text-page-title" style={{ color: 'var(--color-text-primary)' }}>
            Vision
          </h1>
        </div>
        <button
          type="button"
          onClick={() => setCreating(true)}
          className="flex items-center gap-2 px-4 py-2 rounded-md text-body-sm transition-colors"
          style={{
            background: 'var(--color-accent-blue)',
            color: 'var(--color-bg-base)',
            border: 'none',
            cursor: 'pointer',
          }}
        >
          <Plus size={14} aria-hidden="true" />
          New Vision
        </button>
      </div>

      {/* Status tabs */}
      <div
        role="tablist"
        aria-label="Vision status filter"
        className="flex gap-1 mb-6 p-1 rounded-lg"
        style={{
          background: 'var(--color-bg-card)',
          border: '1px solid var(--color-border)',
          width: 'fit-content',
        }}
      >
        {STATUS_TABS.map((tab, idx) => (
          <button
            key={tab}
            ref={(el) => { tabRefs.current[idx] = el }}
            id={`vision-tab-${tab}`}
            type="button"
            role="tab"
            aria-selected={activeTab === tab}
            aria-controls="vision-tabpanel"
            onClick={() => setActiveTab(tab)}
            onKeyDown={(e) => handleTabKeyDown(e, idx)}
            className="px-4 py-1.5 rounded-md text-body-sm transition-colors"
            style={{
              background: activeTab === tab ? 'var(--color-accent-blue)' : 'transparent',
              color: activeTab === tab ? 'var(--color-bg-base)' : 'var(--color-text-muted)',
              border: 'none',
              cursor: 'pointer',
            }}
          >
            {STATUS_LABELS[tab]}
          </button>
        ))}
      </div>

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
          Failed to load vision items. Try refreshing.
        </div>
      )}

      {creating && <CreateVisionForm onClose={() => setCreating(false)} />}

      <div id="vision-tabpanel" role="tabpanel" aria-labelledby={`vision-tab-${activeTab}`}>
        {isLoading ? (
          <div className="space-y-3">
            {Array.from({ length: 4 }, (_, i) => (
              <LoadingSkeleton key={i} className="h-20 w-full" />
            ))}
          </div>
        ) : filtered.length === 0 ? (
          <EmptyState messageKey="common.empty" />
        ) : (
          <div className="space-y-3">
            {filtered.map((item) => (
              <VisionCard key={item.id} item={item} />
            ))}
          </div>
        )}
      </div>
    </div>
  )
}
