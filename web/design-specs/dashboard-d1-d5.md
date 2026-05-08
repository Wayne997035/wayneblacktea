# Dashboard Widgets D1–D5 Design Spec

**Date:** 2026-05-09
**Stack:** React 19 / TypeScript 5.9 / Tailwind CSS v4 (custom properties via `@theme`)
**Design system source:** `web/src/index.css` — all colours, radii, typography referenced by CSS variable name.

---

## Design System Reference

| Token | Value | Usage |
|-------|-------|-------|
| `--color-bg-card` | `#0d1f35` | Card background |
| `--color-bg-hover` | `#112240` | Hover state |
| `--color-border` | `#1a3a5c` | Default border |
| `--color-border-focus` | `#4fc3f7` | Focus ring |
| `--color-text-primary` | `#e8f4f8` | Body copy |
| `--color-text-muted` | `#7a9bb5` | Secondary labels |
| `--color-text-disabled` | `#3d5a73` | Disabled / skeleton base |
| `--color-accent-blue` | `#4fc3f7` | Counts, links, interactive numbers |
| `--color-warning` | `#ff9800` | Amber badge, pending attention |
| `--color-error` | `#f44336` | P1 priority badge |
| `--color-success` | `#4caf50` | Success / online states |
| `--radius-md` | `8px` | Standard card radius |
| `--radius-sm` | `4px` | Badge / chip radius |
| `--radius-full` | `9999px` | Pill badge |

**Typography classes** (from `index.css`):
- `.text-section` — 1rem / 600 — section headings
- `.text-card-title` — 0.875rem / 600 — card headlines
- `.text-body` — 0.875rem / 400 — primary body
- `.text-body-sm` — 0.8125rem / 400 — secondary body, truncated text
- `.text-caption` — 0.75rem / 400 — timestamps, meta
- `.text-label` — 0.6875rem / 500 / uppercase / letter-spacing — section labels

**Skeleton:** use `.skeleton` class from `index.css` (shimmer keyframe already defined).
Global focus ring: `*:focus-visible { outline: 2px solid var(--color-border-focus); outline-offset: 2px; border-radius: var(--radius-sm); }` — no per-component override needed.

---

## Component Hierarchy (all 5 widgets)

```
DashboardPage
├── [left column — 60%]
│   ├── Active Projects (existing)
│   ├── NextTaskCard          [D1] — NEW, below projects
│   └── RecentDecisionsCard   [D2] — NEW, below D1
└── [right column — 40%]
    ├── Weekly Progress (existing)
    ├── HandoffCard (existing)
    ├── PendingProposalsCard  [D3] — NEW, replaces position after HandoffCard
    ├── DueReviewsCard        [D4] — NEW
    ├── RecentKnowledgeCard   [D5] — NEW
    ├── QuickStats (existing)
    └── SystemHealth (existing)
```

Each new widget is self-contained: it owns its own data-fetch hook call and renders its own loading/empty/filled states internally. `DashboardPage` passes no data props; it only passes `onNavigate`-style callbacks.

---

## D1: NextTaskCard

**File:** `web/src/components/dashboard/NextTaskCard.tsx`
**Hook:** `useDashboardNextTask` (new, see Hooks section)

### Props Interface

```typescript
interface NextTaskCardProps {
  onClick: () => void  // navigate('/gtd?task_id=<id>') — caller constructs URL
}
```

The component fetches its own data via `useDashboardNextTask()`. `onClick` is called when the filled card is clicked; the hook data provides the task id so the caller can append it to the URL.

Refined internal signature (the card receives data already resolved):

```typescript
// Internal — not exposed. Component calls hook directly.
// The prop onClick receives task id via closure from DashboardPage:
//   onClick={() => navigate(`/gtd?task_id=${task.id}`)
// DashboardPage wires this at render time once data is available.
```

Simpler approach: the component calls the hook itself and accepts `onNavigate(taskId: string)`.

```typescript
interface NextTaskCardProps {
  onNavigate: (taskId: string) => void
}
```

### Priority Badge Color Map

| priority | label | background token | text token | border token |
|----------|-------|-----------------|-----------|-------------|
| 1 | P1 | `#2e0a0a` (inline) | `--color-error` | `--color-error` |
| 2 | P2 | `#2e1f00` (inline) | `--color-warning` | `--color-warning` |
| 3, 4, 5, or any ≥ 3 | P3+ | `#0a1f35` (inline) | `--color-accent-blue` | `--color-accent-blue` |
| null | — | none | — | — |

Badge contrast check: P1 red `#f44336` on `#2e0a0a` — ratio ≥ 4.5:1. P2 amber `#ff9800` on `#2e1f00` — ratio ≥ 4.5:1. P3+ blue `#4fc3f7` on `#0a1f35` — ratio ≥ 4.5:1.

### States

**Loading:**
```
┌─────────────────────────────────────────┐
│ [skeleton 60px wide, h=14px]            │
│                                         │
│ [skeleton 100% wide, h=16px]            │
│ [skeleton 80% wide, h=12px]  [skel 50px]│
└─────────────────────────────────────────┘
```
Skeleton height: outer card = 76px fixed (matches HandoffCard visual weight).

**Empty (no task):**
```
┌─────────────────────────────────────────┐
│         No pending tasks                │
└─────────────────────────────────────────┘
```
Use `<EmptyState messageKey="dashboard.noNextTask" />`.

**Filled (clickable card):**
```
┌─────────────────────────────────────────┐
│ [P1] Next Task                          │  ← icon(CheckSquare 16px) + label
│                                         │
│ Fix auth token expiry bug               │  ← task title, text-card-title
│ Due: May 12                             │  ← due_date formatted, text-caption muted
└─────────────────────────────────────────┘
```
- Entire card is interactive: `role="button"`, `tabIndex={0}`, `onKeyDown` Enter/Space.
- `aria-label`: `"Next task: {title}, priority {P-label}"`
- Left border accent matches priority color: `borderLeft: '4px solid <priority-color-token>'`
- No left border if priority is null.

### Exact Tailwind + Inline Style Classes

```
// Wrapper (all states)
className="rounded-lg p-4"
style={{ background: 'var(--color-bg-card)', border: '1px solid var(--color-border)' }}

// Filled — add interactive styles + priority left border
style={{
  background: 'var(--color-bg-card)',
  border: '1px solid var(--color-border)',
  borderLeft: '4px solid <priority-color>',
  cursor: 'pointer',
}}
onMouseEnter: e.currentTarget.style.background = 'var(--color-bg-hover)'
onMouseLeave: e.currentTarget.style.background = 'var(--color-bg-card)'

// Header row
className="flex items-center gap-2 mb-2"

// Icon
<CheckSquare size={16} aria-hidden="true" style={{ color: 'var(--color-accent-blue)' }} />

// Section label
className="text-label" style={{ color: 'var(--color-text-muted)' }}

// Priority badge
className="ml-auto rounded font-mono text-caption px-1.5 py-0.5"
style={{ background: <bg>, color: <text>, border: '1px solid <border>' }}

// Task title
className="text-card-title mb-1" style={{ color: 'var(--color-text-primary)' }}

// Due date
className="text-caption" style={{ color: 'var(--color-text-muted)' }}
```

### Sizes

| Element | Size |
|---------|------|
| Card min-height | 76px |
| Icon | 16×16px |
| Priority badge touch target | n/a (not interactive) |
| Card touch target (whole card) | full width × min 44px height |
| Padding | 16px (p-4) |
| Left border (filled) | 4px |

### Accessibility

- `article` element, `aria-label="Next task: {title}"`
- Interactive state: `role="button"` on `article`, `tabIndex={0}`
- Icon: `aria-hidden="true"`
- Priority badge: text is visible — no additional aria needed
- Focus: global `focus-visible` rule applies

---

## D2: RecentDecisionsCard

**File:** `web/src/components/dashboard/RecentDecisionsCard.tsx`
**Hook:** `useDashboardRecentDecisions` (new, see Hooks section)

### Props Interface

```typescript
interface RecentDecisionsCardProps {
  onSeeAll: () => void  // navigate('/decisions')
}
```

Component calls `useDashboardRecentDecisions()` internally.

### States

**Loading (3 skeleton rows):**
```
┌─────────────────────────────────────────┐
│ Recent Decisions              [See all] │
├─────────────────────────────────────────┤
│ [skeleton 70% h=14px]   [skeleton 40px] │
│ [skeleton 40% h=10px]                   │
├─────────────────────────────────────────┤
│ [skeleton 70% h=14px]   [skeleton 40px] │
│ [skeleton 40% h=10px]                   │
├─────────────────────────────────────────┤
│ [skeleton 70% h=14px]   [skeleton 40px] │
│ [skeleton 40% h=10px]                   │
└─────────────────────────────────────────┘
```

**Empty:**
```
┌─────────────────────────────────────────┐
│ Recent Decisions                        │
│         No decisions yet               │
└─────────────────────────────────────────┘
```

**Filled (max 3 rows):**
```
┌─────────────────────────────────────────┐
│ RECENT DECISIONS              [See all] │
├─────────────────────────────────────────┤
│ Adopt pgvector for embeddings  [chatbot]│
│ 2 days ago                              │
├─────────────────────────────────────────┤
│ Switch to Railway for hosting           │
│ 5 days ago                              │
├─────────────────────────────────────────┤
│ Use FSRS for spaced repetition [wbt]    │
│ 1 week ago                              │
└─────────────────────────────────────────┘
```

- Each row is NOT individually clickable (no per-row navigation).
- "See all" button → `onSeeAll()`.
- `repo_name` shown as monospace badge (same as HandoffCard repo badge pattern).
- Timestamp: relative ("2 days ago") using a `formatRelative` utility or `Intl.RelativeTimeFormat`.
- Rows separated by `border-b` using `--color-border`.

### Row Structure

```
// Card container
className="rounded-lg"
style={{ background: 'var(--color-bg-card)', border: '1px solid var(--color-border)' }}

// Header row (inside p-4, no bottom border)
className="flex items-center justify-between px-4 pt-4 pb-3"

// Header label
className="text-label" style={{ color: 'var(--color-text-muted)' }}

// "See all" button
className="text-caption rounded px-2 py-1 transition-colors"
style={{ color: 'var(--color-accent-blue)', background: 'transparent' }}
hover: style.color → 'var(--color-text-primary)', style.background → 'var(--color-bg-hover)'

// Decision row (div, not button — no per-row nav)
className="px-4 py-3"
style={{ borderTop: '1px solid var(--color-border)' }}

// Title line
className="flex items-center justify-between gap-2 mb-0.5"

// Title text
className="text-body truncate" style={{ color: 'var(--color-text-primary)' }}

// Repo badge
className="shrink-0 font-mono text-caption rounded px-1.5 py-0.5"
style={{
  color: 'var(--color-accent-blue)',
  background: 'var(--color-bg-hover)',
  border: '1px solid var(--color-border)',
}}

// Timestamp
className="text-caption" style={{ color: 'var(--color-text-muted)' }}
```

### Sizes

| Element | Size |
|---------|------|
| Row padding | 12px top/bottom (py-3), 16px left/right (px-4) |
| Repo badge | auto-width, min-width 0, shrink-0 |
| Card border-radius | var(--radius-md) = 8px |
| "See all" button min touch | 44px height not required (supplemental control) |

### Accessibility

- `section` with `aria-labelledby` pointing to header text id.
- "See all" is a `<button>` element, `aria-label="See all decisions"`.
- Decision rows: `role="listitem"` inside `role="list"` wrapper.
- Title text: `title` attribute for truncated text overflow.

---

## D3: PendingProposalsCard

**File:** `web/src/components/dashboard/PendingProposalsCard.tsx`
**Hook:** reuse existing `usePendingProposals()` from `web/src/hooks/usePendingProposals.ts` — no new hook needed.

### Props Interface

```typescript
interface PendingProposalsCardProps {
  onClick: () => void  // navigate('/proposals')
}
```

### Count Badge Thresholds

| count | badge style |
|-------|------------|
| 0 | no badge — card in "empty / all caught up" state |
| 1–9 | amber badge: bg `#2e1f00`, text `--color-warning`, border `--color-warning` |
| ≥ 10 | same amber badge with text "10+" |

### States

**Loading:**
```
┌─────────────────────────────────────────┐
│ [skeleton 50% h=14px]    [skeleton 24px]│
│ [skeleton 80% h=12px]                   │
│ [skeleton 60% h=12px]                   │
└─────────────────────────────────────────┘
```

**Empty (count = 0):**
```
┌─────────────────────────────────────────┐
│ PENDING PROPOSALS                       │
│         All caught up                   │
└─────────────────────────────────────────┘
```
Border: normal `--color-border`.

**Filled (count > 0) — clickable card:**
```
┌──────────────────────────────────────────┐
│ PENDING PROPOSALS               [3]      │  ← amber count badge
│                                          │
│  concept  concept  task                  │  ← first 3 proposal type chips
└──────────────────────────────────────────┘
```
- Entire card is interactive when count > 0.
- Left border: `4px solid var(--color-warning)`.
- `aria-label="Pending proposals: {count} awaiting review"`.

### Type Chip Colors

| type | text color token | bg |
|------|-----------------|-----|
| concept | `--color-accent-blue` | `--color-bg-hover` |
| goal | `--color-success` | `#0a2e0a` (inline) |
| project | `--color-accent-purple` | inline `#1a0a35` |
| task | `--color-warning` | `#2e1f00` (inline) |

### Exact Classes

```
// Wrapper (filled, interactive)
className="rounded-lg p-4 transition-colors"
style={{
  background: 'var(--color-bg-card)',
  border: '1px solid var(--color-border)',
  borderLeft: '4px solid var(--color-warning)',
  cursor: 'pointer',
}}

// Count badge
className="rounded-full font-mono text-caption px-2 py-0.5"
style={{ background: '#2e1f00', color: 'var(--color-warning)', border: '1px solid var(--color-warning)' }}

// Type chip
className="rounded text-caption px-1.5 py-0.5"
style={{ background: <bg>, color: <text> }}

// Chip row
className="flex items-center gap-2 mt-3 flex-wrap"
```

### Accessibility

- `article` element.
- When count > 0: `role="button"`, `tabIndex={0}`, `aria-label`.
- `onKeyDown` Enter/Space triggers `onClick`.
- Count badge: `aria-label="3 pending proposals"` on the badge span.
- Type chips: `aria-hidden="true"` (decorative — the aria-label on the card covers the count).

---

## D4: DueReviewsCard

**File:** `web/src/components/dashboard/DueReviewsCard.tsx`
**Hook:** `useDueReviewsDashboard` (new — thin wrapper, see Hooks section)

### Props Interface

```typescript
interface DueReviewsCardProps {
  onClick: () => void  // navigate('/reviews')
}
```

### States

**Loading:**
```
┌─────────────────────────────────────────┐
│ [skeleton 50% h=14px]    [skeleton 24px]│
│ [skeleton 90% h=12px]                   │
│ [skeleton 75% h=12px]                   │
│ [skeleton 80% h=12px]                   │
└─────────────────────────────────────────┘
```

**Empty (0 reviews due):**
```
┌─────────────────────────────────────────┐
│ DUE REVIEWS                             │
│         All reviews done                │
└─────────────────────────────────────────┘
Normal border, no left accent.
```

**Filled:**
```
┌─────────────────────────────────────────┐
│ DUE REVIEWS                   [5 due]   │  ← blue count badge
│                                         │
│  • FSRS algorithm                       │
│  • Postgres indexing strategies         │
│  • Go interface embedding               │
│  + 2 more                               │  ← if total > 3
└─────────────────────────────────────────┘
```
- Show first 3 concept titles, then "+ N more" if total > 3.
- Entire card clickable: `role="button"`, `tabIndex={0}`.
- Left border: `4px solid var(--color-accent-blue)`.

### Count Badge

```
className="rounded-full font-mono text-caption px-2 py-0.5"
style={{
  background: '#0a1f35',
  color: 'var(--color-accent-blue)',
  border: '1px solid var(--color-accent-blue)',
}}
```

### Exact Classes

```
// Card (filled, interactive)
className="rounded-lg p-4 transition-colors"
style={{
  background: 'var(--color-bg-card)',
  border: '1px solid var(--color-border)',
  borderLeft: '4px solid var(--color-accent-blue)',
  cursor: 'pointer',
}}

// Header row
className="flex items-center justify-between mb-3"

// Label
className="text-label" style={{ color: 'var(--color-text-muted)' }}

// Concept list
className="flex flex-col gap-1"

// Concept row
className="flex items-center gap-2"

// Bullet dot
className="w-1.5 h-1.5 rounded-full shrink-0"
style={{ background: 'var(--color-accent-blue)' }}

// Concept title
className="text-body-sm truncate" style={{ color: 'var(--color-text-primary)' }}

// "+ N more" line
className="text-caption mt-1" style={{ color: 'var(--color-text-muted)' }}
```

### Sizes

| Element | Size |
|---------|------|
| Bullet dot | 6×6px (w-1.5 h-1.5) |
| Card touch target | full width × min 44px |
| Max items shown | 3, then "+N more" |

### Accessibility

- `article`, `aria-label="Due reviews: {count} concepts due"`.
- When interactive: `role="button"`, `tabIndex={0}`, `onKeyDown` Enter/Space.
- Concept list: `role="list"` / `role="listitem"`.
- Bullet dots: `aria-hidden="true"`.

---

## D5: RecentKnowledgeCard

**File:** `web/src/components/dashboard/RecentKnowledgeCard.tsx`
**Hook:** `useRecentKnowledge` (new, see Hooks section)

### Props Interface

```typescript
interface RecentKnowledgeCardProps {
  onClick: () => void  // navigate('/knowledge')
}
```

### States

**Loading (3 skeleton rows):**
```
┌─────────────────────────────────────────┐
│ [skeleton 60% h=14px]                   │
│ [skeleton 90% h=14px]  [sk 40px][sk30px]│
│ [skeleton 80% h=14px]  [sk 50px]        │
│ [skeleton 70% h=14px]  [sk 35px][sk45px]│
└─────────────────────────────────────────┘
```

**Empty:**
```
┌─────────────────────────────────────────┐
│ RECENT KNOWLEDGE                        │
│         No knowledge items              │
└─────────────────────────────────────────┘
```

**Filled:**
```
┌──────────────────────────────────────────┐
│ RECENT KNOWLEDGE                         │
├──────────────────────────────────────────┤
│ Adaptive FSRS spacing algorithm   [til]  │
│ [spaced-rep] [learning]                  │
├──────────────────────────────────────────┤
│ Railway deployment gotchas        [art]  │
│ [railway] [devops]                       │
├──────────────────────────────────────────┤
│ Go embed directive                [til]  │
│ (no tags)                                │
└──────────────────────────────────────────┘
```
- Each row has: title (truncated) + type badge (right) + tag chips below.
- Entire card is the click target → `onClick()`.

### Type Badge Map

| KnowledgeType | abbrev | color |
|--------------|--------|-------|
| article | art | `--color-accent-purple` |
| til | til | `--color-success` |
| bookmark | bkm | `--color-warning` |
| zettelkasten | ztl | `--color-accent-blue` |

```
// Type badge
className="shrink-0 rounded font-mono text-caption px-1 py-0.5"
style={{ color: <color>, background: 'var(--color-bg-hover)', border: '1px solid var(--color-border)' }}
```

### Tag Chips

```
// Tag chip
className="rounded text-caption px-1.5 py-0.5"
style={{
  background: 'var(--color-bg-hover)',
  color: 'var(--color-text-muted)',
  border: '1px solid var(--color-border)',
}}
```
Max 3 tags per item. If `tags.length > 3`, show first 2 + `+N` chip.

### Exact Card Classes

```
// Card wrapper (clickable)
className="rounded-lg transition-colors"
style={{
  background: 'var(--color-bg-card)',
  border: '1px solid var(--color-border)',
  cursor: 'pointer',
}}
role="button" tabIndex={0}
onMouseEnter/Leave → toggle var(--color-bg-hover)
onKeyDown Enter/Space → onClick

// Header
className="px-4 pt-4 pb-3"

// Row
className="px-4 py-3"
style={{ borderTop: '1px solid var(--color-border)' }}

// Title line
className="flex items-center justify-between gap-2 mb-1"

// Title text
className="text-body truncate" style={{ color: 'var(--color-text-primary)' }}

// Tags line
className="flex items-center gap-1.5 flex-wrap"
```

### Accessibility

- `article`, `aria-label="Recent knowledge items"`.
- `role="button"`, `tabIndex={0}` on article when interactive.
- `onKeyDown` Enter/Space → `onClick`.
- Tag chips and type badges: `aria-hidden="true"` (decorative — title is the meaningful label).
- `title` attribute on truncated title text elements.

---

## DashboardPage Integration

### Column Assignment

```typescript
// LEFT column (60%) — after existing ProjectCard section:
<section>
  <div className="text-label mb-3" style={{ color: 'var(--color-text-muted)' }}>
    {t('dashboard.sections.nextTask')}
  </div>
  <NextTaskCard onNavigate={(id) => navigate(`/gtd?task_id=${id}`)} />
</section>

<section>
  <RecentDecisionsCard onSeeAll={() => navigate('/decisions')} />
</section>

// RIGHT column (40%) — insert between HandoffCard and QuickStats:
<section>
  <PendingProposalsCard onClick={() => navigate('/proposals')} />
</section>

<section>
  <DueReviewsCard onClick={() => navigate('/reviews')} />
</section>

<section>
  <RecentKnowledgeCard onClick={() => navigate('/knowledge')} />
</section>
```

### Import additions to DashboardPage.tsx

```typescript
import { NextTaskCard } from '../components/dashboard/NextTaskCard'
import { RecentDecisionsCard } from '../components/dashboard/RecentDecisionsCard'
import { PendingProposalsCard } from '../components/dashboard/PendingProposalsCard'
import { DueReviewsCard } from '../components/dashboard/DueReviewsCard'
import { RecentKnowledgeCard } from '../components/dashboard/RecentKnowledgeCard'
```

No new state or data-fetching logic in `DashboardPage` — each widget is self-sufficient.

---

## New Hooks

All hooks live in `web/src/hooks/`. All use `apiFetch` from `../lib/api` matching existing pattern.

### useDashboardNextTask

```typescript
// web/src/hooks/useDashboardNextTask.ts
import { useQuery } from '@tanstack/react-query'
import { apiFetch } from '../lib/api'

export interface NextTask {
  id: string
  title: string
  priority: number | null
  status: string
  due_date?: string | null
}

export interface NextTaskResponse {
  task: NextTask | null
}

export function useDashboardNextTask() {
  return useQuery<NextTaskResponse>({
    queryKey: ['dashboard', 'next-task'],
    queryFn: () => apiFetch<NextTaskResponse>('/api/dashboard/next-task'),
    staleTime: 60_000,
  })
}
```

### useDashboardRecentDecisions

```typescript
// web/src/hooks/useDashboardRecentDecisions.ts
import { useQuery } from '@tanstack/react-query'
import { apiFetch } from '../lib/api'
import type { Decision } from '../types/api'

export function useDashboardRecentDecisions() {
  return useQuery<Decision[]>({
    queryKey: ['dashboard', 'recent-decisions'],
    queryFn: () => apiFetch<Decision[]>('/api/dashboard/recent-decisions?limit=3'),
    staleTime: 120_000,
  })
}
```

### usePendingProposals (existing — reuse)

Already exists at `web/src/hooks/usePendingProposals.ts`. `PendingProposalsCard` imports and calls `usePendingProposals()` directly. No new hook needed.

### useDueReviewsDashboard

```typescript
// web/src/hooks/useDueReviewsDashboard.ts
import { useQuery } from '@tanstack/react-query'
import { apiFetch } from '../lib/api'
import type { DueReview } from '../types/api'

export function useDueReviewsDashboard() {
  return useQuery<DueReview[]>({
    queryKey: ['dashboard', 'due-reviews'],
    queryFn: () => apiFetch<DueReview[]>('/api/learning/reviews?limit=50'),
    staleTime: 120_000,
  })
}
```

Note: reuses the same `/api/learning/reviews` endpoint as `useReviews()` but uses a separate queryKey (`['dashboard', 'due-reviews']`) so dashboard cache and reviews page cache are independently managed. staleTime 2 min to avoid double-fetching on page entry.

### useRecentKnowledge

```typescript
// web/src/hooks/useRecentKnowledge.ts
import { useQuery } from '@tanstack/react-query'
import { apiFetch } from '../lib/api'
import type { KnowledgeItem } from '../types/api'

export function useRecentKnowledge() {
  return useQuery<KnowledgeItem[]>({
    queryKey: ['dashboard', 'recent-knowledge'],
    queryFn: () => apiFetch<KnowledgeItem[]>('/api/knowledge?limit=3'),
    staleTime: 120_000,
  })
}
```

---

## Responsive Behavior

All 5 widgets follow the existing `DashboardPage` grid: `grid-cols-1 lg:grid-cols-[60%_40%]`.

| Viewport | Behavior |
|----------|----------|
| mobile (< lg) | Single column — all sections stack, D1-D5 appear in DOM order after existing sections |
| tablet / desktop (≥ lg) | Two-column layout as described in Component Hierarchy above |

No widget has internal responsive variants — all cards are fluid-width within their column.

Text truncation (`truncate` / `-webkit-line-clamp`) is on by default to prevent overflow at narrow widths.

---

## Design Decisions

1. **Self-contained data fetching per widget** — each widget owns its hook call rather than receiving data via props from `DashboardPage`. This matches the pattern established by `QuickStats` (which calls `useDashboardStats` externally but could easily own it). It keeps `DashboardPage` as a layout shell.

2. **Priority badge uses inline bg hex values** — the design system does not define semantic background tokens for priority levels (only text/border color tokens exist for error/warning/info). Inline values `#2e0a0a` / `#2e1f00` / `#0a1f35` match the existing pattern used in `DashboardPage`'s error banner and `--color-status-*-bg` tokens. These dark-tinted versions of the accent color pass WCAG AA contrast against their text tokens.

3. **No per-row click on RecentDecisionsCard** — the API endpoint is a summary view (`/api/dashboard/recent-decisions`). Decisions have no individual detail page. Navigation is to the list page only.

4. **PendingProposalsCard reuses `usePendingProposals`** — the existing hook already targets the correct endpoint and staleTime. Creating a separate dashboard hook would cause cache duplication with the Proposals page.

5. **DueReviewsCard uses separate queryKey from useReviews** — `['dashboard', 'due-reviews']` vs `['reviews']`. This prevents the dashboard's staleTime (2 min) from suppressing a fresh fetch when the user navigates to the Reviews page (which should always show current data).

6. **`formatRelative` needed for timestamps** — none of the existing components implement relative time formatting. Frontend engineer must add a small utility (`web/src/lib/formatRelative.ts`) or use `Intl.RelativeTimeFormat` inline. Exact implementation is left to engineer; the spec requires "X days ago / X hours ago / just now" output.

7. **Skeleton dimensions are explicit** — to prevent layout shift. Heights specified in each state description should be applied via `style={{ height: '<n>px' }}` on `LoadingSkeleton` (same pattern as DashboardPage lines 79, 105, 126).
