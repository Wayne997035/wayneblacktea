# D1 — DecisionsPage: Date Picker + Repo Filter + Search + Second-precision Timestamp

**Task:** `7146b26d`
**Decision references:** `81ff22e7` (picker + 3-month default + second-precision ISO timestamp)
**File touched:** `web/src/pages/DecisionsPage.tsx`, `web/src/components/decisions/DecisionEntry.tsx`, `web/src/hooks/useDecisions.ts`
**New components:** `DateRangePicker.tsx`, `RepoFilterChip.tsx` (or extend existing `<select>`)

---

## 1. User journey

1. User opens `/decisions` → page loads with **last 90 days** of decisions by default (was: full list).
2. User glances at the timeline; each entry shows **YYYY-MM-DD HH:MM:SS** (was: just date) so duplicate-titled decisions in the same day are distinguishable.
3. User clicks the date-range chip ("Last 90 days ▾") → popover opens with presets (7d / 30d / 90d / All) **and** a manual `from`–`to` date input pair.
4. User picks "Last 30 days" (or types a custom range) → list re-filters client-side; URL updates to `?from=2026-01-28&to=2026-04-28` (shareable).
5. User picks **Repo: chatbot-go** from the new repo dropdown → list narrows further. Existing project dropdown still works in parallel.
6. User types in the search box → existing client-side full-text search (over `title` + `rationale` + `context` + `decision`) — extend to also search `decision` and `context` fields.

---

## 2. Layout sketch

### Desktop (≥ 1024 px, max-width 1200 px)

```
┌──────────────────────────────────────────────────────────────────────┐
│ Decisions                                                            │
├──────────────────────────────────────────────────────────────────────┤
│ ┌─────────────┐ ┌──────────────┐ ┌──────────────┐ ┌───────────────┐ │
│ │ 📅 90 days ▾│ │ All projects ▾│ │ All repos ▾  │ │🔍 Search…    │ │
│ └─────────────┘ └──────────────┘ └──────────────┘ └───────────────┘ │
│  ↑ NEW          existing           ↑ NEW           extended search   │
│                                                                      │
│ Showing 23 decisions · Last 90 days · All projects · All repos  [↻] │
│                                                                      │
│ │● 2026-04-28 14:32:07  Decision title here                          │
│ │  Rationale clamped to 2 lines… [Show full]                         │
│ │                                                                    │
│ │● 2026-04-26 09:15:42  Another decision                             │
│ │  …                                                                 │
└──────────────────────────────────────────────────────────────────────┘
```

### Mobile (< 640 px)

```
┌─────────────────────────────┐
│ Decisions                   │
├─────────────────────────────┤
│ [📅 90 days ▾]              │ ← full-width row 1
│ [All projects ▾]            │ ← full-width row 2
│ [All repos ▾]               │ ← full-width row 3
│ [🔍 Search…              ]  │ ← full-width row 4
│                             │
│ 23 decisions · 90d          │
│                             │
│ │● 2026-04-28 14:32:07      │
│ │  Title                    │
│ │  Rationale (2 lines)…     │
│ │  [Show full]              │
└─────────────────────────────┘
```

Filter row uses `flex flex-wrap` with `gap-3`; on mobile each control wraps to its own line.

---

## 3. Component tree

```
DecisionsPage
├── DateRangePicker             ← NEW (presets + custom from/to)
├── <select> projectFilter      ← reuse existing
├── <select> repoFilter         ← NEW (mirror projectFilter; built from useRepos())
├── SearchInput                 ← reuse existing inline input
├── ResultSummaryBar            ← NEW (small text: "N decisions · range · filters")
├── ErrorBanner                 ← reuse existing inline error block
├── LoadingSkeleton (×4)        ← reuse
├── EmptyState                  ← reuse
└── DecisionTimeline
    └── DecisionEntry           ← MODIFY: timestamp formatter
```

**Reuse:** `DecisionTimeline`, `EmptyState`, `LoadingSkeleton`.
**New:**
- `DateRangePicker.tsx` (controlled component, emits `{ from: Date | null, to: Date | null, presetKey: string }`)
- `ResultSummaryBar.tsx` (pure presentational; props: `count`, `rangeLabel`, `filtersActive: boolean`, `onReset`).

**Modify:**
- `DecisionEntry.tsx` — replace `formatDate` with `formatTimestamp` returning `YYYY-MM-DD HH:MM:SS` (locale-independent ISO, second precision; uses `date.toISOString().slice(0,19).replace('T',' ')`).
- `useDecisions.ts` — accept optional `{ from?: string; to?: string }` query params; pass to backend as ISO date strings (see §6) **OR** keep client-side filtering only (recommended for MVP — backend already returns full list and filtering 1k rows in browser is cheap).

---

## 4. State / data shape

```ts
// DecisionsPage local state
const [range, setRange] = useState<{ from: Date | null; to: Date | null; presetKey: '7d'|'30d'|'90d'|'all'|'custom' }>({
  from: subDays(new Date(), 90),
  to: new Date(),
  presetKey: '90d',
})
const [projectFilter, setProjectFilter] = useState<string>('all')
const [repoFilter, setRepoFilter] = useState<string>('all')
const [search, setSearch] = useState('')

// URL sync via useSearchParams (react-router-dom v7)
// Read on mount; write on change (debounced 200 ms for search).
```

**Hooks:**
- `useDecisions()` — already returns `Decision[]` via TanStack Query, **keep as-is**. Filtering happens client-side.
- `useProjects()` — for project dropdown options (existing).
- `useRepos()` — **new use**, for repo dropdown options. Already exported from `web/src/hooks/useRepos.ts`.

**Filtering pipeline (memoized):**
```ts
const filtered = useMemo(() => (decisions ?? []).filter((d) => {
  const created = new Date(d.created_at)
  if (range.from && created < range.from) return false
  if (range.to && created > endOfDay(range.to)) return false
  if (projectFilter !== 'all' && d.project_id !== projectFilter) return false
  if (repoFilter !== 'all' && d.repo_name !== repoFilter) return false
  if (search) {
    const q = search.toLowerCase()
    const haystack = `${d.title} ${d.rationale} ${d.context} ${d.decision} ${d.alternatives ?? ''}`.toLowerCase()
    if (!haystack.includes(q)) return false
  }
  return true
}), [decisions, range, projectFilter, repoFilter, search])
```

---

## 5. Edge cases

| State | Render |
|-------|--------|
| `isLoading` | 4 × `LoadingSkeleton h-20 w-full` inside the timeline border-l container (existing). Filters disabled (greyed `opacity-50 pointer-events-none`). |
| `isError` | Existing red error banner: `error.loadFailed` translation. Filters remain interactive. |
| `decisions.length === 0` (no data at all) | `<EmptyState messageKey="decisions.noDecisions" />` (existing). |
| `filtered.length === 0` after filtering | `<EmptyState messageKey="decisions.noResults" ctaLabelKey="decisions.resetFilters" onCta={resetAll} />`. NEW i18n key. |
| `range.from > range.to` (invalid custom range) | DateRangePicker shows inline error `decisions.invalidRange`; "Apply" button disabled; list keeps last valid filter. |
| URL has `?from=invalid` | Silently fall back to default 90 days; do not crash. |
| `decision.repo_name === null` AND `repoFilter === 'all'` | Include — null treated as "any repo". |
| `decision.repo_name === null` AND `repoFilter !== 'all'` | Exclude. |

---

## 6. Backend contract

**No new endpoint required for MVP.** Existing `GET /api/decisions` already returns the full list including `created_at` (RFC3339 with seconds, e.g. `2026-04-28T14:32:07.123Z`) and optional `repo_name` / `project_id`.

**Field shape (`Decision`, from `web/src/types/api.ts`):**
```ts
{
  id: string
  project_id?: string | null
  repo_name?: string | null   // ← used by NEW repo filter
  title: string
  context: string
  decision: string
  rationale: string
  alternatives?: string | null
  created_at: string  // RFC3339 with sub-second precision; UI truncates to seconds
}
```

**Future optimisation (NOT this PR):** if list grows past 5k rows, add server-side `?from=&to=&repo=&project=&q=` params to `/api/decisions`. Mark as backlog.

---

## 7. Tailwind class palette

| Purpose | Class / inline-style token |
|---------|---------------------------|
| Page wrapper | `p-6 max-w-[1200px] mx-auto` |
| Filter row | `flex flex-wrap items-center gap-3 mb-6` |
| Filter chip / select | `rounded-md px-3 py-2 h-9 text-body` + `var(--color-bg-input)` bg + `var(--color-border)` border |
| Active filter chip border | `var(--color-border-focus)` |
| Search input width | `w-[224px]` desktop, `w-full` mobile |
| Result summary bar | `text-caption mb-4` + `var(--color-text-muted)` |
| Timeline rail | `border-l-2 ml-4 pl-4` + `var(--color-border)` |
| Timestamp label | `text-caption font-mono` + `var(--color-text-muted)` |
| Reset link | `text-caption underline-offset-2` + `var(--color-accent-blue)` |
| DateRangePicker popover | `rounded-lg p-4 shadow-lg` + `var(--color-bg-card)` + `border var(--color-border)`; positioned `absolute top-full mt-2 left-0 z-30` |
| Preset button (selected) | `bg-[var(--color-accent-blue)] text-[var(--color-bg-base)]` |
| Preset button (idle) | `bg-[var(--color-bg-input)] text-[var(--color-text-primary)] hover:bg-[var(--color-bg-hover)]` |

---

## 8. Accessibility

- DateRangePicker trigger: `<button aria-haspopup="dialog" aria-expanded={open} aria-controls="date-range-popover">`. Popover is `role="dialog" aria-label="Date range"`. Trap focus while open; `Esc` closes.
- Manual date inputs: `<input type="date">` with `aria-label="From date"` / `aria-label="To date"` and `min` / `max` cross-bound to enforce `from ≤ to`.
- Repo filter: `<select aria-label="Filter by repository">`; chip pattern same as projectFilter.
- Result summary bar: `aria-live="polite"` so screen readers announce count changes ("23 decisions, last 90 days").
- Timeline entry timestamp: wrap in `<time dateTime={d.created_at}>` for semantic meaning; visible text is the truncated `YYYY-MM-DD HH:MM:SS` form.
- Tab order: date range → project → repo → search → first decision entry.
- All filters keyboard reachable (no JS-only click handlers). Existing global `*:focus-visible` outline applies.
- Reduced motion: popover open/close animation respects `prefers-reduced-motion`; use no transition or instant fade.

---

## 9. Acceptance criteria

- [ ] Default load fetches `/api/decisions` and shows only entries from last 90 days (URL → `?from=YYYY-MM-DD&to=YYYY-MM-DD` reflected).
- [ ] Each timeline entry shows timestamp as `YYYY-MM-DD HH:MM:SS` (24-hour, second precision) wrapped in `<time>` element.
- [ ] DateRangePicker offers presets `7d / 30d / 90d / All` plus a custom `from`–`to` input pair; selecting a preset updates URL params and re-filters within ≤ 200 ms.
- [ ] Repo filter dropdown lists all unique `repo_name` values from `useRepos()` (alphabetical, plus "All repos" first); selection narrows decisions client-side.
- [ ] Search now matches `title`, `rationale`, `context`, `decision`, `alternatives` (case-insensitive substring).
- [ ] When all filters yield 0 results, page shows `EmptyState` with a "Reset filters" CTA that restores defaults.
- [ ] Result summary bar (`23 decisions · Last 90 days · 2 filters active`) updates on every filter change and is announced via `aria-live="polite"`.
- [ ] Mobile (< 640 px): each filter occupies its own row, taps target ≥ 44 × 44 px.
- [ ] Keyboard: `Tab` cycles all controls in visual order; `Esc` closes date picker popover; `Enter` on filter chip opens popover.
- [ ] `npm run lint` and `npm run build` exit clean (0 type errors, 0 lint warnings).

---

## ⚠️ NEW DEPENDENCY (optional)

`react-day-picker@^9` for the calendar grid in DateRangePicker. **Justification:** rolling a date-range calendar with keyboard nav from scratch is ~300 LOC; react-day-picker is unstyled, ~13 KB gzipped, well-maintained, and integrates cleanly with our token system via CSS variables.

**If frontend-engineer prefers zero deps:** fall back to two `<input type="date">` elements side-by-side inside the popover. Lose the visual calendar grid but keep all functionality. **Acceptable for MVP.**
