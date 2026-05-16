interface StaleBadgeProps {
  stale: boolean
}

/**
 * StaleBadge renders an orange pill "Stale — reconcile needed" only when
 * stale_badge is true (backend constant: reconcile_dashboard not called
 * within the last 24h, or never called).
 */
export function StaleBadge({ stale }: StaleBadgeProps) {
  if (!stale) return null

  return (
    <span
      className="inline-flex items-center rounded-full px-2 py-0.5 text-caption font-medium"
      style={{
        background: '#3d1f00',
        color: 'var(--color-warning)',
        border: '1px solid var(--color-warning)',
      }}
      aria-label="Dashboard data may be stale — reconcile needed"
    >
      Stale — reconcile needed
    </span>
  )
}
