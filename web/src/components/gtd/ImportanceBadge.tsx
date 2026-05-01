interface ImportanceBadgeProps {
  importance: number | null | undefined
  dimmed?: boolean
}

export function ImportanceBadge({ importance, dimmed = false }: ImportanceBadgeProps) {
  if (importance == null) return null

  const level = Math.max(1, Math.min(3, Math.round(importance))) as 1 | 2 | 3

  const colorMap: Record<1 | 2 | 3, { bg: string; color: string }> = {
    1: { bg: 'var(--color-error)',   color: '#ffffff' },
    2: { bg: 'var(--color-warning)', color: 'var(--color-bg-base)' },
    3: { bg: 'var(--color-success)', color: '#ffffff' },
  }

  const { bg, color } = colorMap[level]

  return (
    <span
      className="inline-flex items-center justify-center w-4 h-4 rounded-full text-[10px] font-mono font-bold shrink-0"
      style={{ background: bg, color, opacity: dimmed ? 0.5 : 1 }}
      aria-label={`Importance ${level} of 3`}
    >
      <span aria-hidden="true">{level}</span>
    </span>
  )
}
