export function formatDueDate(due: string): string {
  const date = new Date(due)
  const now = new Date()

  const todayStart = new Date(now.getFullYear(), now.getMonth(), now.getDate())
  const dateStart = new Date(date.getFullYear(), date.getMonth(), date.getDate())

  const diff = dateStart.getTime() - todayStart.getTime()

  if (diff === 0) return 'Today'
  if (diff === 86_400_000) return 'Tomorrow'
  return date.toLocaleDateString('en', { month: 'short', day: 'numeric' })
}
