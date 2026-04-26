import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

interface GoalProgressProps {
  completed: number;
  total: number;
}

export function GoalProgress({ completed, total }: GoalProgressProps) {
  const { t } = useTranslation()
  const [animated, setAnimated] = useState(false)
  const isMounted = useRef(false)

  const r = 34 // radius — slightly smaller to fit in 80px with stroke-width 6
  const cx = 40
  const cy = 40
  const circumference = 2 * Math.PI * r
  const pct = total > 0 ? completed / total : 0
  const offset = animated ? circumference * (1 - pct) : circumference

  useEffect(() => {
    if (!isMounted.current) {
      isMounted.current = true
      // Trigger animation after mount
      requestAnimationFrame(() => {
        requestAnimationFrame(() => setAnimated(true))
      })
    }
  }, [])

  const percentLabel = total > 0 ? `${Math.round(pct * 100)}%` : '—'

  return (
    <div
      className="flex flex-col items-center gap-2"
      role="img"
      aria-label={`Weekly progress: ${completed} of ${total} tasks completed`}
    >
      <svg width="80" height="80" viewBox="0 0 80 80" aria-hidden="true">
        {/* Track */}
        <circle
          cx={cx}
          cy={cy}
          r={r}
          fill="none"
          stroke="var(--color-border)"
          strokeWidth="6"
        />
        {/* Fill */}
        <circle
          cx={cx}
          cy={cy}
          r={r}
          fill="none"
          stroke="var(--color-accent-blue)"
          strokeWidth="6"
          strokeLinecap="round"
          strokeDasharray={circumference}
          strokeDashoffset={offset}
          style={{ transition: 'stroke-dashoffset 600ms ease-out' }}
          transform="rotate(-90 40 40)"
        />
        {/* Center text */}
        <text
          x={cx}
          y={cy}
          textAnchor="middle"
          dominantBaseline="central"
          style={{
            fill: 'var(--color-text-primary)',
            fontSize: '14px',
            fontWeight: 600,
            fontFamily: 'var(--font-mono)',
          }}
        >
          {percentLabel}
        </text>
      </svg>
      <span className="text-caption" style={{ color: 'var(--color-text-muted)' }}>
        {total > 0 ? `${completed} / ${total} ${t('dashboard.tasksDone')}` : '—'}
      </span>
    </div>
  )
}
