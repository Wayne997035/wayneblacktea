/**
 * Formats a date string or Date into a relative time string using Intl.RelativeTimeFormat.
 * Examples: "just now", "5 minutes ago", "2 hours ago", "3 days ago", "2 weeks ago"
 */
export function formatRelative(dateInput: string | Date): string {
  const date = typeof dateInput === 'string' ? new Date(dateInput) : dateInput
  const now = new Date()
  const diffMs = date.getTime() - now.getTime()
  const diffSeconds = Math.round(diffMs / 1000)
  const absDiffSeconds = Math.abs(diffSeconds)

  const rtf = new Intl.RelativeTimeFormat('en', { numeric: 'auto' })

  if (absDiffSeconds < 60) {
    return 'just now'
  }

  const absDiffMinutes = Math.round(absDiffSeconds / 60)
  if (absDiffMinutes < 60) {
    return rtf.format(-absDiffMinutes, 'minute')
  }

  const absDiffHours = Math.round(absDiffMinutes / 60)
  if (absDiffHours < 24) {
    return rtf.format(-absDiffHours, 'hour')
  }

  const absDiffDays = Math.round(absDiffHours / 24)
  if (absDiffDays < 7) {
    return rtf.format(-absDiffDays, 'day')
  }

  const absDiffWeeks = Math.round(absDiffDays / 7)
  if (absDiffWeeks < 5) {
    return rtf.format(-absDiffWeeks, 'week')
  }

  const absDiffMonths = Math.round(absDiffDays / 30)
  if (absDiffMonths < 12) {
    return rtf.format(-absDiffMonths, 'month')
  }

  const absDiffYears = Math.round(absDiffDays / 365)
  return rtf.format(-absDiffYears, 'year')
}
