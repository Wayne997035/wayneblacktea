/**
 * Formats a date string or Date into a relative time string using Intl.RelativeTimeFormat.
 * Examples: "just now", "5 minutes ago", "2 hours ago", "3 days ago", "2 weeks ago"
 */
export function formatRelative(dateInput: string | Date | null | undefined): string {
  if (!dateInput) return '—'
  const date = typeof dateInput === 'string' ? new Date(dateInput) : dateInput
  if (isNaN(date.getTime())) return '—'

  const now = new Date()
  const diffMs = date.getTime() - now.getTime()
  const diffSeconds = Math.round(diffMs / 1000)
  const absDiffSeconds = Math.abs(diffSeconds)

  const rtf = new Intl.RelativeTimeFormat('en', { numeric: 'auto' })

  if (absDiffSeconds < 60) {
    return 'just now'
  }

  const diffMinutes = Math.round(diffSeconds / 60)
  if (Math.abs(diffMinutes) < 60) {
    return rtf.format(diffMinutes, 'minute')
  }

  const diffHours = Math.round(diffMinutes / 60)
  if (Math.abs(diffHours) < 24) {
    return rtf.format(diffHours, 'hour')
  }

  const diffDays = Math.round(diffHours / 24)
  if (Math.abs(diffDays) < 7) {
    return rtf.format(diffDays, 'day')
  }

  const diffWeeks = Math.round(diffDays / 7)
  if (Math.abs(diffWeeks) < 5) {
    return rtf.format(diffWeeks, 'week')
  }

  const diffMonths = Math.round(diffDays / 30)
  if (Math.abs(diffMonths) < 12) {
    return rtf.format(diffMonths, 'month')
  }

  const diffYears = Math.round(diffDays / 365)
  return rtf.format(diffYears, 'year')
}
