import { useState, useRef, useEffect } from 'react'

export type SourceType = 'article' | 'til' | 'bookmark' | 'zettelkasten' | 'agent-proposed'

interface SourceBadgeProps {
  type: SourceType
  /** Title of the originating knowledge item */
  sourceTitle?: string
  /** Content preview for tooltip (truncated to 100 chars internally) */
  sourceContent?: string
  /** knowledge item id — used for deep-link on click */
  sourceItemId?: string
}

// [F0925-25]
const SOURCE_CONFIG: Record<
  SourceType,
  { icon: string; label: string; bg: string; color: string; border: string }
> = {
  article: {
    icon: '📄',
    label: 'Article',
    bg: 'var(--color-source-article-bg)',
    color: 'var(--color-accent-blue)',
    border: 'var(--color-source-article-border)',
  },
  til: {
    icon: '💡',
    label: 'TIL',
    bg: 'var(--color-source-til-bg)',
    color: 'var(--color-source-til-text)',
    border: 'var(--color-source-til-border)',
  },
  bookmark: {
    icon: '🔖',
    label: 'Bookmark',
    bg: 'var(--color-source-bookmark-bg)',
    color: 'var(--color-source-bookmark-text)',
    border: 'var(--color-source-bookmark-border)',
  },
  zettelkasten: {
    icon: '📚',
    label: 'Note',
    bg: 'var(--color-source-note-bg)',
    color: 'var(--color-source-note-text)',
    border: 'var(--color-source-note-border)',
  },
  'agent-proposed': {
    icon: '🤖',
    label: 'Agent',
    bg: 'var(--color-source-agent-bg)',
    color: 'var(--color-source-agent-text)',
    border: 'var(--color-source-agent-border)',
  },
}

export function SourceBadge({ type, sourceTitle, sourceContent, sourceItemId }: SourceBadgeProps) {
  const config = SOURCE_CONFIG[type]
  const [showTooltip, setShowTooltip] = useState(false)
  const tooltipRef = useRef<HTMLDivElement>(null)
  const triggerRef = useRef<HTMLButtonElement>(null)

  // Hide tooltip on outside click
  useEffect(() => {
    if (!showTooltip) return
    function onPointerDown(e: PointerEvent) {
      if (
        triggerRef.current && !triggerRef.current.contains(e.target as Node) &&
        tooltipRef.current && !tooltipRef.current.contains(e.target as Node)
      ) {
        setShowTooltip(false)
      }
    }
    document.addEventListener('pointerdown', onPointerDown)
    return () => document.removeEventListener('pointerdown', onPointerDown)
  }, [showTooltip])

  const hasTooltipContent = Boolean(sourceTitle || sourceContent)
  const contentPreview = sourceContent ? sourceContent.slice(0, 100) + (sourceContent.length > 100 ? '…' : '') : ''

  const UUID_RE = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i

  function handleClick() {
    if (sourceItemId && UUID_RE.test(sourceItemId)) {
      const url = `/knowledge?id=${encodeURIComponent(sourceItemId)}`
      window.open(url, '_self')
    } else if (hasTooltipContent) {
      setShowTooltip((v) => !v)
    }
  }

  return (
    <span className="relative inline-flex items-center">
      <button
        ref={triggerRef}
        type="button"
        onClick={handleClick}
        onMouseEnter={() => hasTooltipContent && setShowTooltip(true)}
        onMouseLeave={() => setShowTooltip(false)}
        aria-label={`Source: ${config.label}${sourceTitle ? ` — ${sourceTitle}` : ''}`}
        className="inline-flex items-center gap-1 rounded px-2 py-0.5 text-label transition-opacity hover:opacity-80"
        style={{
          background: config.bg,
          color: config.color,
          border: `1px solid ${config.border}`,
          cursor: sourceItemId || hasTooltipContent ? 'pointer' : 'default',
          minHeight: '20px',
        }}
      >
        <span aria-hidden="true">{config.icon}</span>
        {config.label}
      </button>

      {/* Tooltip */}
      {showTooltip && hasTooltipContent && (
        <div
          ref={tooltipRef}
          role="tooltip"
          className="absolute z-50 rounded-md p-3 text-body-sm"
          style={{
            bottom: 'calc(100% + 6px)',
            left: 0,
            minWidth: '200px',
            maxWidth: '300px',
            background: 'var(--color-bg-card)',
            border: '1px solid var(--color-border)',
            color: 'var(--color-text-primary)',
            boxShadow: '0 4px 16px var(--color-overlay-40)', // [F0925-25]
            pointerEvents: 'none',
          }}
        >
          {sourceTitle && (
            <p className="text-card-title mb-1" style={{ color: 'var(--color-text-primary)' }}>
              {sourceTitle}
            </p>
          )}
          {contentPreview && (
            <p style={{ color: 'var(--color-text-muted)' }}>{contentPreview}</p>
          )}
          {sourceItemId && (
            <p className="text-caption mt-1" style={{ color: 'var(--color-accent-blue)' }}>
              Click to open source
            </p>
          )}
        </div>
      )}
    </span>
  )
}
