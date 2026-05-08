import { useSearchParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { useLearningHistory } from '../hooks/useLearningHistory'
import { useLearningStats } from '../hooks/useLearningStats'
import { LoadingSkeleton } from '../components/ui/LoadingSkeleton'
import type { ConceptHistoryItem, ConceptLearningStatus } from '../types/api'

// ----- types / constants -----

type FilterStatus = 'all' | 'new' | 'learning' | 'reviewing' | 'mastered'

const FILTER_OPTIONS: { key: FilterStatus; labelKey: string }[] = [
  { key: 'all',       labelKey: 'learningHistory.filterAll' },
  { key: 'new',       labelKey: 'learningHistory.filterNew' },
  { key: 'learning',  labelKey: 'learningHistory.filterLearning' },
  { key: 'reviewing', labelKey: 'learningHistory.filterReviewing' },
  { key: 'mastered',  labelKey: 'learningHistory.filterMastered' },
]

const VALID_FILTERS = new Set<string>(FILTER_OPTIONS.map((f) => f.key))

function isValidFilter(s: string | null): s is FilterStatus {
  return s !== null && VALID_FILTERS.has(s)
}

// ----- Heatmap helpers -----

/**
 * Build a map of "YYYY-MM-DD" -> count from last_reviewed_at dates.
 */
function buildDayCountMap(items: ConceptHistoryItem[]): Map<string, number> {
  const map = new Map<string, number>()
  for (const item of items) {
    if (item.last_reviewed_at) {
      const day = item.last_reviewed_at.slice(0, 10)
      map.set(day, (map.get(day) ?? 0) + 1)
    }
  }
  return map
}

/**
 * Generate the last 90 calendar days as Date objects (oldest first).
 */
function last90Days(): Date[] {
  const days: Date[] = []
  const today = new Date()
  today.setHours(0, 0, 0, 0)
  for (let i = 89; i >= 0; i--) {
    const d = new Date(today)
    d.setDate(today.getDate() - i)
    days.push(d)
  }
  return days
}

function toIsoDate(d: Date): string {
  return d.toISOString().slice(0, 10)
}

/** Returns 0–4 intensity level for a count. */
function heatLevel(count: number): 0 | 1 | 2 | 3 | 4 {
  if (count === 0) return 0
  if (count === 1) return 1
  if (count === 2) return 2
  if (count <= 4) return 3
  return 4
}

const HEAT_COLORS: Record<0 | 1 | 2 | 3 | 4, string> = {
  0: 'var(--color-bg-hover)',
  1: '#1b4332',
  2: '#2d6a4f',
  3: '#40916c',
  4: '#52b788',
}

// ----- Status badge -----

const STATUS_STYLES: Record<ConceptLearningStatus, { bg: string; color: string; label: string }> = {
  new:       { bg: 'var(--color-status-archived-bg)',   color: 'var(--color-status-archived-text)',  label: 'New' },
  learning:  { bg: 'var(--color-status-completed-bg)',  color: 'var(--color-status-completed-text)', label: 'Learning' },
  reviewing: { bg: 'var(--color-status-on-hold-bg)',    color: 'var(--color-status-on-hold-text)',   label: 'Reviewing' },
  mastered:  { bg: 'var(--color-status-active-bg)',     color: 'var(--color-status-active-text)',    label: 'Mastered' },
}

// ----- Sub-components -----

interface StatsBarProps {
  totalReviews: number | null
  streakDays: number | null
  mastered: number | null
  totalConcepts: number | null
  isLoading: boolean
}

function StatsBar({ totalReviews, streakDays, mastered, totalConcepts, isLoading }: StatsBarProps) {
  const { t } = useTranslation()

  if (isLoading) {
    return (
      <div className="grid grid-cols-3 gap-4 mb-6">
        {[0, 1, 2].map((i) => (
          <LoadingSkeleton key={i} className="h-16 w-full rounded-lg" />
        ))}
      </div>
    )
  }

  const stats = [
    { label: t('learningHistory.statsTotal'), value: totalReviews ?? '—' },
    { label: t('learningHistory.statsStreak'), value: streakDays !== null ? `${streakDays}d` : '—' },
    {
      label: t('learningHistory.statsMastered'),
      value: mastered !== null && totalConcepts !== null ? `${mastered}/${totalConcepts}` : '—',
    },
  ]

  return (
    <div className="grid grid-cols-3 gap-4 mb-6">
      {stats.map((s) => (
        <div
          key={s.label}
          className="rounded-lg p-4 text-center"
          style={{
            background: 'var(--color-bg-card)',
            border: '1px solid var(--color-border)',
          }}
        >
          <div
            className="text-section mb-1"
            style={{ color: 'var(--color-accent-blue)' }}
          >
            {s.value}
          </div>
          <div className="text-label" style={{ color: 'var(--color-text-muted)' }}>
            {s.label}
          </div>
        </div>
      ))}
    </div>
  )
}

interface HeatmapProps {
  items: ConceptHistoryItem[]
}

function Heatmap({ items }: HeatmapProps) {
  const { t } = useTranslation()
  const days = last90Days()
  const countMap = buildDayCountMap(items)

  // Pad days to align to start on Monday (0=Sun, 1=Mon, ...)
  // Find the day-of-week of the first day (0=Sun) and shift to Mon=0 system
  const firstDay = days[0]
  const rawDow = firstDay.getDay() // 0=Sun
  // We want Mon=0..Sun=6. If rawDow=0 (Sun), padding=6; if rawDow=1 (Mon), padding=0
  const padding = rawDow === 0 ? 6 : rawDow - 1
  const paddingCells = Array.from({ length: padding })

  return (
    <div
      className="rounded-lg p-4 mb-6"
      style={{
        background: 'var(--color-bg-card)',
        border: '1px solid var(--color-border)',
      }}
    >
      <div className="text-label mb-3" style={{ color: 'var(--color-text-muted)' }}>
        {t('learningHistory.heatmapTitle')}
      </div>
      {/* Day-of-week header */}
      <div
        style={{
          display: 'grid',
          gridTemplateColumns: 'repeat(7, 1fr)',
          gap: '3px',
          marginBottom: '3px',
        }}
      >
        {['M', 'T', 'W', 'T', 'F', 'S', 'S'].map((d, i) => (
          <div
            key={i}
            className="text-caption text-center"
            style={{ color: 'var(--color-text-muted)' }}
          >
            {d}
          </div>
        ))}
      </div>
      {/* Grid */}
      <div
        style={{
          display: 'grid',
          gridTemplateColumns: 'repeat(7, 1fr)',
          gap: '3px',
        }}
        role="grid"
        aria-label={t('learningHistory.heatmapTitle')}
      >
        {/* Padding cells */}
        {paddingCells.map((_, i) => (
          <div key={`pad-${i}`} style={{ aspectRatio: '1', borderRadius: '2px' }} />
        ))}
        {/* Day cells */}
        {days.map((day) => {
          const dateStr = toIsoDate(day)
          const count = countMap.get(dateStr) ?? 0
          const level = heatLevel(count)
          const label = count > 0
            ? `${dateStr}: ${count} review${count > 1 ? 's' : ''}`
            : dateStr
          return (
            <div
              key={dateStr}
              role="gridcell"
              title={label}
              aria-label={label}
              style={{
                aspectRatio: '1',
                borderRadius: '2px',
                background: HEAT_COLORS[level],
                cursor: count > 0 ? 'default' : undefined,
                transition: 'background 150ms ease',
              }}
            />
          )
        })}
      </div>
      {/* Legend */}
      <div className="flex items-center gap-2 mt-3 justify-end">
        <span className="text-caption" style={{ color: 'var(--color-text-muted)' }}>Less</span>
        {([0, 1, 2, 3, 4] as const).map((level) => (
          <div
            key={level}
            style={{
              width: '12px',
              height: '12px',
              borderRadius: '2px',
              background: HEAT_COLORS[level],
            }}
          />
        ))}
        <span className="text-caption" style={{ color: 'var(--color-text-muted)' }}>More</span>
      </div>
    </div>
  )
}

interface ConceptCardProps {
  item: ConceptHistoryItem
}

function ConceptCard({ item }: ConceptCardProps) {
  const { t } = useTranslation()
  const style = STATUS_STYLES[item.status]

  const nextReviewStr = item.next_review_at
    ? new Date(item.next_review_at).toLocaleDateString(undefined, { month: 'short', day: 'numeric' })
    : null

  const firstDate = item.first_reviewed_at ? new Date(item.first_reviewed_at) : null
  const daysSinceFirst = firstDate
    ? Math.floor((Date.now() - firstDate.getTime()) / 86_400_000)
    : null

  return (
    <div
      className="rounded-lg p-4"
      style={{
        background: 'var(--color-bg-card)',
        border: '1px solid var(--color-border)',
      }}
    >
      <div className="flex items-start justify-between gap-3 mb-2">
        <h3 className="text-card-title flex-1" style={{ color: 'var(--color-text-primary)' }}>
          {item.title}
        </h3>
        <span
          className="text-label rounded-full px-2 py-0.5 shrink-0"
          style={{ background: style.bg, color: style.color }}
        >
          {style.label}
        </span>
      </div>

      {/* Tags */}
      {item.tags.length > 0 && (
        <div className="flex flex-wrap gap-1 mb-2">
          {item.tags.map((tag) => (
            <span
              key={tag}
              className="text-label rounded-full px-2 py-0.5"
              style={{
                background: 'var(--color-bg-hover)',
                color: 'var(--color-text-muted)',
              }}
            >
              #{tag}
            </span>
          ))}
        </div>
      )}

      {/* Meta row */}
      <div className="flex items-center gap-3 flex-wrap">
        <span className="text-caption" style={{ color: 'var(--color-text-muted)' }}>
          {t('learningHistory.conceptReviewCount', { count: item.review_count })}
        </span>
        {daysSinceFirst !== null && (
          <span className="text-caption" style={{ color: 'var(--color-text-muted)' }}>
            {t('learningHistory.conceptDaysLearning', { days: daysSinceFirst })}
          </span>
        )}
        {nextReviewStr && (
          <span className="text-caption" style={{ color: 'var(--color-accent-blue)' }}>
            {t('learningHistory.conceptNextReview', { date: nextReviewStr })}
          </span>
        )}
      </div>
    </div>
  )
}

// ----- Main page -----

export function LearningHistoryPage() {
  const { t } = useTranslation()
  const [searchParams, setSearchParams] = useSearchParams()

  const rawFilter = searchParams.get('filter')
  const activeFilter: FilterStatus = isValidFilter(rawFilter) ? rawFilter : 'all'

  function setFilter(f: FilterStatus) {
    setSearchParams((prev) => {
      const next = new URLSearchParams(prev)
      next.set('filter', f)
      return next
    })
  }

  const historyQuery = useLearningHistory(activeFilter === 'all' ? undefined : activeFilter)
  const statsQuery = useLearningStats()

  const items = historyQuery.data ?? []

  return (
    <div className="p-6 max-w-[960px] mx-auto">
      {/* Page title */}
      <h1 className="text-section mb-6" style={{ color: 'var(--color-text-primary)' }}>
        {t('learningHistory.title')}
      </h1>

      {/* Stats bar */}
      <StatsBar
        isLoading={statsQuery.isLoading}
        totalReviews={statsQuery.data?.total_reviews ?? null}
        streakDays={statsQuery.data?.streak_days ?? null}
        mastered={statsQuery.data?.mastered ?? null}
        totalConcepts={statsQuery.data?.total_concepts ?? null}
      />

      {/* 90-day heatmap (all items, not filtered) */}
      {historyQuery.isLoading ? (
        <LoadingSkeleton className="h-40 w-full rounded-lg mb-6" />
      ) : (
        <Heatmap items={items} />
      )}

      {/* Filter tabs */}
      <div
        role="tablist"
        aria-label="Learning status filter"
        className="flex gap-1 mb-4 flex-wrap"
      >
        {FILTER_OPTIONS.map((opt) => {
          const isActive = activeFilter === opt.key
          return (
            <button
              key={opt.key}
              role="tab"
              type="button"
              aria-selected={isActive}
              onClick={() => setFilter(opt.key)}
              className="rounded-md px-3 py-1.5 text-body-sm transition-colors"
              style={{
                background: isActive ? 'var(--color-accent-blue)' : 'var(--color-bg-hover)',
                color: isActive ? 'var(--color-bg-base)' : 'var(--color-text-muted)',
                border: 'none',
                cursor: 'pointer',
                fontWeight: isActive ? 600 : 400,
              }}
            >
              {t(opt.labelKey)}
            </button>
          )
        })}
      </div>

      {/* Concept list */}
      {historyQuery.isLoading ? (
        <div className="flex flex-col gap-3">
          {[0, 1, 2, 3].map((i) => (
            <LoadingSkeleton key={i} className="h-24 w-full rounded-lg" />
          ))}
        </div>
      ) : historyQuery.isError ? (
        <div
          className="rounded-md p-3 text-body-sm"
          style={{
            background: '#2e0a0a',
            border: '1px solid var(--color-error)',
            color: 'var(--color-error)',
          }}
          role="alert"
        >
          {t('error.loadFailed')}
        </div>
      ) : items.length === 0 ? (
        <div
          className="text-body-sm py-12 text-center"
          style={{ color: 'var(--color-text-muted)' }}
        >
          {t('learningHistory.noItems')}
        </div>
      ) : (
        <div className="flex flex-col gap-3">
          {items.map((item) => (
            <ConceptCard key={item.id} item={item} />
          ))}
        </div>
      )}
    </div>
  )
}
