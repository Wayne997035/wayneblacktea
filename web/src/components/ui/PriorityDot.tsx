interface PriorityDotProps {
  level: 1 | 2 | 3 | 4 | 5;
  showLabel?: boolean;
}

const priorityColors: Record<1 | 2 | 3 | 4 | 5, string> = {
  1: 'var(--color-priority-1)',
  2: 'var(--color-priority-2)',
  3: 'var(--color-priority-3)',
  4: 'var(--color-priority-4)',
  5: 'var(--color-priority-5)',
}

export function PriorityDot({ level, showLabel = false }: PriorityDotProps) {
  return (
    <span aria-label={`Priority ${level}`} className="inline-flex items-center gap-1 shrink-0">
      <span
        className="w-2.5 h-2.5 rounded-full inline-block shrink-0"
        style={{ background: priorityColors[level] }}
      />
      {showLabel && (
        <span className="text-caption" style={{ color: 'var(--color-text-muted)' }}>
          {level}
        </span>
      )}
    </span>
  )
}
