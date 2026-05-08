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

const SOURCE_CONFIG: Record<
  SourceType,
  { icon: string; label: string; bg: string; color: string; border: string }
> = {
  article: {
    icon: '📄',
    label: 'Article',
    bg: 'rgba(21, 101, 192, 0.15)',
    color: '#4fc3f7',
    border: 'rgba(79, 195, 247, 0.4)',
  },
  til: {
    icon: '💡',
    label: 'TIL',
    bg: 'rgba(46, 125, 50, 0.15)',
    color: '#66bb6a',
    border: 'rgba(102, 187, 106, 0.4)',
  },
  bookmark: {
    icon: '🔖',
    label: 'Bookmark',
    bg: 'rgba(81, 45, 168, 0.15)',
    color: '#ba68c8',
    border: 'rgba(186, 104, 200, 0.4)',
  },
  zettelkasten: {
    icon: '📚',
    label: 'Note',
    bg: 'rgba(230, 81, 0, 0.15)',
    color: '#ffb74d',
    border: 'rgba(255, 183, 77, 0.4)',
  },
  'agent-proposed': {
    icon: '🤖',
    label: 'Agent',
    bg: 'rgba(96, 96, 96, 0.15)',
    color: '#9e9e9e',
    border: 'rgba(158, 158, 158, 0.4)',
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
            boxShadow: '0 4px 16px rgba(0,0,0,0.4)',
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
