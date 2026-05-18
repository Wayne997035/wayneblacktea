import { useState } from 'react'
import { Plus } from 'lucide-react'
import { useVision, useCreateVision, useUpdateVision } from '../hooks/useVision'
import { LoadingSkeleton } from '../components/ui/LoadingSkeleton'
import { EmptyState } from '../components/ui/EmptyState'
import type { VisionItemSummary, VisionStatus } from '../types/api'

// --- Status configuration ---

const STATUS_TABS: VisionStatus[] = ['open', 'discussing', 'maturing', 'promoted', 'dismissed']

const STATUS_LABELS: Record<VisionStatus, string> = {
  open:       'Open',
  discussing: 'Discussing',
  maturing:   'Maturing',
  promoted:   'Promoted',
  dismissed:  'Dismissed',
}

const STATUS_STYLE: Record<VisionStatus, { bg: string; text: string }> = {
  open:       { bg: 'var(--color-status-active-bg)',    text: 'var(--color-status-active-text)' },
  discussing: { bg: 'var(--color-status-on-hold-bg)',   text: 'var(--color-status-on-hold-text)' },
  maturing:   { bg: 'var(--color-accent-blue-bg, rgba(88,166,255,.15))', text: 'var(--color-accent-blue)' },
  promoted:   { bg: 'var(--color-status-completed-bg)', text: 'var(--color-status-completed-text)' },
  dismissed:  { bg: 'var(--color-status-archived-bg)',  text: 'var(--color-status-archived-text)' },
}

// --- Sub-components ---

interface StatusBadgeProps {
  status: VisionStatus
}

function VisionStatusBadge({ status }: StatusBadgeProps) {
  const { bg, text } = STATUS_STYLE[status]
  return (
    <span
      className="px-2 py-0.5 rounded-full font-mono whitespace-nowrap uppercase"
      style={{ background: bg, color: text, fontSize: '10px' }}
    >
      {STATUS_LABELS[status]}
    </span>
  )
}

interface VisionCardProps {
  item: VisionItemSummary
}

function VisionCard({ item }: VisionCardProps) {
  const { mutate: update, isPending } = useUpdateVision()

  function handleStatusChange(e: React.ChangeEvent<HTMLSelectElement>) {
    update({ id: item.id, data: { status: e.target.value as VisionStatus } })
  }

  return (
    <article
      className="rounded-lg p-4"
      style={{
        background: 'var(--color-bg-card)',
        border: '1px solid var(--color-border)',
      }}
    >
      <div className="flex items-start justify-between gap-3 mb-2">
        <VisionStatusBadge status={item.status} />
        <span className="text-caption shrink-0" style={{ color: 'var(--color-text-muted)' }}>
          {new Date(item.created_at).toLocaleDateString(undefined, {
            month: 'short',
            day: 'numeric',
          })}
        </span>
      </div>

      <p className="text-body font-medium mb-1" style={{ color: 'var(--color-text-primary)' }}>
        {item.title}
      </p>

      {item.why_blocked && (
        <p
          className="text-body-sm mb-3"
          style={{
            color: 'var(--color-text-muted)',
            display: '-webkit-box',
            WebkitLineClamp: 3,
            WebkitBoxOrient: 'vertical',
            overflow: 'hidden',
          }}
        >
          {item.why_blocked}
        </p>
      )}

      {item.parent_initiative && (
        <p className="text-caption mb-2" style={{ color: 'var(--color-text-muted)' }}>
          Initiative: {item.parent_initiative}
        </p>
      )}

      <div className="flex items-center gap-2 mt-3">
        <label
          htmlFor={`status-${item.id}`}
          className="text-caption shrink-0"
          style={{ color: 'var(--color-text-muted)' }}
        >
          Status:
        </label>
        <select
          id={`status-${item.id}`}
          value={item.status}
          onChange={handleStatusChange}
          disabled={isPending}
          className="rounded-md px-2 py-1 text-body-sm"
          style={{
            background: 'var(--color-bg-input)',
            border: '1px solid var(--color-border)',
            color: 'var(--color-text-primary)',
            cursor: isPending ? 'not-allowed' : 'pointer',
            opacity: isPending ? 0.6 : 1,
          }}
          aria-label={`Change status for ${item.title}`}
        >
          {STATUS_TABS.map((s) => (
            <option key={s} value={s}>
              {STATUS_LABELS[s]}
            </option>
          ))}
        </select>
      </div>
    </article>
  )
}

// --- Create form ---

interface CreateVisionFormProps {
  onClose: () => void
}

function CreateVisionForm({ onClose }: CreateVisionFormProps) {
  const [title, setTitle] = useState('')
  const [whyBlocked, setWhyBlocked] = useState('')
  const [titleError, setTitleError] = useState('')
  const [whyError, setWhyError] = useState('')
  const { mutate: create, isPending } = useCreateVision()

  function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    let valid = true

    if (!title.trim()) {
      setTitleError('Title is required')
      valid = false
    } else {
      setTitleError('')
    }

    if (!whyBlocked.trim()) {
      setWhyError('Why blocked is required')
      valid = false
    } else {
      setWhyError('')
    }

    if (!valid) return

    create(
      { title: title.trim(), why_blocked: whyBlocked.trim() },
      { onSuccess: onClose },
    )
  }

  const inputStyle: React.CSSProperties = {
    background: 'var(--color-bg-input)',
    border: '1px solid var(--color-border)',
    color: 'var(--color-text-primary)',
  }

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center p-4"
      style={{ background: 'rgba(0,0,0,0.5)' }}
      role="dialog"
      aria-modal="true"
      aria-label="Create vision item"
    >
      <div
        className="rounded-lg p-6 w-full max-w-md"
        style={{ background: 'var(--color-bg-card)', border: '1px solid var(--color-border)' }}
      >
        <h2 className="text-page-title mb-4" style={{ color: 'var(--color-text-primary)' }}>
          New Vision Item
        </h2>

        <form onSubmit={handleSubmit} noValidate>
          <div className="mb-4">
            <label
              htmlFor="vision-title"
              className="block text-body-sm mb-1"
              style={{ color: 'var(--color-text-primary)' }}
            >
              Title <span aria-hidden="true" style={{ color: 'var(--color-error)' }}>*</span>
            </label>
            <input
              id="vision-title"
              type="text"
              value={title}
              onChange={(e) => setTitle(e.target.value)}
              required
              aria-required="true"
              aria-describedby={titleError ? 'vision-title-error' : undefined}
              className="w-full rounded-md px-3 py-2 text-body"
              style={inputStyle}
            />
            {titleError && (
              <p id="vision-title-error" className="text-caption mt-1" style={{ color: 'var(--color-error)' }} role="alert">
                {titleError}
              </p>
            )}
          </div>

          <div className="mb-6">
            <label
              htmlFor="vision-why-blocked"
              className="block text-body-sm mb-1"
              style={{ color: 'var(--color-text-primary)' }}
            >
              Why Blocked <span aria-hidden="true" style={{ color: 'var(--color-error)' }}>*</span>
            </label>
            <textarea
              id="vision-why-blocked"
              value={whyBlocked}
              onChange={(e) => setWhyBlocked(e.target.value)}
              required
              aria-required="true"
              aria-describedby={whyError ? 'vision-why-error' : undefined}
              rows={4}
              className="w-full rounded-md px-3 py-2 text-body resize-none"
              style={inputStyle}
              placeholder="What is currently preventing this from being actionable?"
            />
            {whyError && (
              <p id="vision-why-error" className="text-caption mt-1" style={{ color: 'var(--color-error)' }} role="alert">
                {whyError}
              </p>
            )}
          </div>

          <div className="flex justify-end gap-3">
            <button
              type="button"
              onClick={onClose}
              className="px-4 py-2 rounded-md text-body-sm"
              style={{
                background: 'var(--color-bg-input)',
                border: '1px solid var(--color-border)',
                color: 'var(--color-text-muted)',
                cursor: 'pointer',
              }}
            >
              Cancel
            </button>
            <button
              type="submit"
              disabled={isPending}
              className="px-4 py-2 rounded-md text-body-sm"
              style={{
                background: 'var(--color-accent-blue)',
                color: 'var(--color-bg-base)',
                border: 'none',
                cursor: isPending ? 'not-allowed' : 'pointer',
                opacity: isPending ? 0.6 : 1,
              }}
            >
              {isPending ? 'Creating…' : 'Create'}
            </button>
          </div>
        </form>
      </div>
    </div>
  )
}

// --- Page ---

type StatusFilter = VisionStatus | 'all'

const FILTER_TABS: StatusFilter[] = ['all', ...STATUS_TABS]

const FILTER_LABELS: Record<StatusFilter, string> = {
  all: 'All',
  ...STATUS_LABELS,
}

export function VisionPage() {
  const [activeTab, setActiveTab] = useState<StatusFilter>('all')
  const [showCreate, setShowCreate] = useState(false)

  const { data: items, isLoading, isError } = useVision(
    activeTab === 'all' ? undefined : activeTab,
  )

  return (
    <div className="p-6 max-w-[900px] mx-auto">
      <div className="flex items-center justify-between mb-4">
        <h1 className="text-page-title" style={{ color: 'var(--color-text-primary)' }}>
          Vision
        </h1>
        <button
          type="button"
          onClick={() => setShowCreate(true)}
          className="flex items-center gap-2 px-4 py-2 rounded-md text-body-sm transition-colors"
          style={{
            background: 'var(--color-accent-blue)',
            color: 'var(--color-bg-base)',
            border: 'none',
            cursor: 'pointer',
          }}
          aria-label="Create new vision item"
        >
          <Plus size={14} aria-hidden="true" />
          New Vision
        </button>
      </div>

      {/* Status tabs */}
      <div
        role="tablist"
        aria-label="Vision status filter"
        className="flex flex-wrap gap-1 mb-6 p-1 rounded-lg"
        style={{
          background: 'var(--color-bg-card)',
          border: '1px solid var(--color-border)',
          width: 'fit-content',
        }}
      >
        {FILTER_TABS.map((tab) => (
          <button
            key={tab}
            id={`vision-tab-${tab}`}
            type="button"
            role="tab"
            aria-selected={activeTab === tab}
            aria-controls="vision-tabpanel"
            onClick={() => setActiveTab(tab)}
            className="px-4 py-1.5 rounded-md text-body-sm transition-colors"
            style={{
              background: activeTab === tab ? 'var(--color-accent-blue)' : 'transparent',
              color: activeTab === tab ? 'var(--color-bg-base)' : 'var(--color-text-muted)',
              border: 'none',
              cursor: 'pointer',
            }}
          >
            {FILTER_LABELS[tab]}
          </button>
        ))}
      </div>

      {isError && (
        <div
          className="rounded-md p-3 mb-6 text-body-sm"
          style={{
            background: 'var(--color-error-bg, #2e0a0a)',
            border: '1px solid var(--color-error)',
            color: 'var(--color-error)',
          }}
          role="alert"
        >
          Failed to load vision items.
        </div>
      )}

      <div id="vision-tabpanel" role="tabpanel" aria-labelledby={`vision-tab-${activeTab}`}>
        {isLoading ? (
          <div className="space-y-3">
            {Array.from({ length: 4 }, (_, i) => (
              <LoadingSkeleton key={i} className="h-24 w-full" />
            ))}
          </div>
        ) : !items || items.length === 0 ? (
          <EmptyState messageKey="vision.empty" />
        ) : (
          <div className="space-y-3">
            {items.map((item) => (
              <VisionCard key={item.id} item={item} />
            ))}
          </div>
        )}
      </div>

      {showCreate && <CreateVisionForm onClose={() => setShowCreate(false)} />}
    </div>
  )
}
