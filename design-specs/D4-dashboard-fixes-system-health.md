# D4 — Dashboard: Fix "Today's Decisions" + Wire Pending-task Click → /gtd + Add SystemHealthCard

**Task:** `23e05576`
**Files touched:** `web/src/pages/DashboardPage.tsx`, `web/src/components/dashboard/QuickStats.tsx`
**New components:** `web/src/components/dashboard/SystemHealthCard.tsx`, optional `web/src/hooks/useSystemHealth.ts`, `web/src/hooks/useTodayDecisionsCount.ts`
**Touches GTD page:** `web/src/pages/GtdPage.tsx` (read `?task=ID` param, scroll/highlight matched task)

---

## 1. User journey

1. User opens `/` (Dashboard) → greeting + date row appears (existing).
2. **Fix #1:** Right column's "Decisions today" stat (currently shows `—`) now shows the **actual count** of decisions made today (00:00 → 23:59 local time).
3. **Fix #2:** "Pending tasks" stat is currently a number-only cell — make the entire cell a **clickable Link** that navigates to `/gtd`. Hover changes background; cursor becomes pointer.
4. **New:** Below QuickStats, a **SystemHealthCard** appears showing:
   - API ping status (green = healthy, red = degraded; reuse `useApiPing()`),
   - Database connection (from new `/api/system/health`),
   - Last successful sync time,
   - Optional: queued background jobs count.
5. **Bonus interaction:** clicking a pending task on the Dashboard (TODO list, not yet defined visually) navigates to `/gtd?task=<id>`. On `/gtd`, the matched task auto-expands and its row scrolls into view with a 1.5s highlight pulse.

---

## 2. Layout sketch

### Desktop (≥ 1024 px)

```
┌──────────────────────────────────────────────────────────────────────┐
│ Good morning                                  Wednesday, Apr 28 2026 │
├─────────────────────────────────────┬────────────────────────────────┤
│ ACTIVE PROJECTS                     │ WEEKLY PROGRESS                │
│ ┌─────────────────────────────────┐ │       ╭───╮                   │
│ │ ● wayneblacktea     active      │ │      ╱  68% ╲                  │
│ │   Personal OS for…              │ │     │       │                  │
│ │   feature/p0-conv…              │ │      ╲     ╱                   │
│ └─────────────────────────────────┘ │       ╰───╯                    │
│ ┌─────────────────────────────────┐ │       17 / 25 done             │
│ │ ● chatbot-go        active      │ │                                │
│ └─────────────────────────────────┘ │ NEXT SESSION                   │
│                                     │ ┌──────────────────────────┐  │
│                                     │ │ ⚡ Wire SystemHealthCard │  │
│                                     │ └──────────────────────────┘  │
│                                     │                                │
│                                     │ ┌────────────┬─────────────┐  │
│                                     │ │ Pending    │ Decisions   │  │
│                                     │ │   12   →   │   3   ✓     │  │  ← cells now clickable
│                                     │ │ tasks      │ today       │  │
│                                     │ └────────────┴─────────────┘  │
│                                     │                                │
│                                     │ SYSTEM HEALTH                  │  ← NEW
│                                     │ ┌──────────────────────────┐  │
│                                     │ │ ● API           healthy  │  │
│                                     │ │ ● DB            healthy  │  │
│                                     │ │ ● Last sync   2m ago     │  │
│                                     │ └──────────────────────────┘  │
└─────────────────────────────────────┴────────────────────────────────┘
```

### Mobile (< 640 px)

```
┌─────────────────────────────┐
│ Good morning   Apr 28, 2026 │
├─────────────────────────────┤
│ ACTIVE PROJECTS             │
│ [card] [card] [card]        │
├─────────────────────────────┤
│ WEEKLY PROGRESS             │
│       ╭───╮                 │
│      ╱ 68% ╲                │
│       ╰───╯                 │
│       17 / 25               │
├─────────────────────────────┤
│ NEXT SESSION                │
│ ⚡ Wire SystemHealthCard…   │
├─────────────────────────────┤
│ ┌──────────┬──────────────┐ │
│ │ 12 →     │ 3 ✓          │ │
│ │ Pending  │ Decisions    │ │
│ └──────────┴──────────────┘ │
├─────────────────────────────┤
│ SYSTEM HEALTH               │
│ ● API     healthy           │
│ ● DB      healthy           │
│ ● Last sync   2m ago        │
└─────────────────────────────┘
```

---

## 3. Component tree

```
DashboardPage
├── greeting + date row (existing)
├── error banner (existing)
├── grid lg:grid-cols-[60%_40%]
│   ├── Active Projects section (existing)
│   └── right column
│       ├── Weekly Progress section (existing)
│       ├── Next Session / Handoff section (existing)
│       ├── QuickStats                      ← MODIFY: cells become Links
│       │   ├── StatCell (Pending tasks → Link to="/gtd")
│       │   └── StatCell (Decisions today → Link to="/decisions?from=TODAY&to=TODAY")
│       └── SystemHealthCard                ← NEW
│           ├── HealthRow ×3-4 (API / DB / Sync / Queue)
│           └── overall pill (top-right)
```

**New:**
- `SystemHealthCard.tsx` — props: none (calls `useSystemHealth()` internally). Layout: rounded card, section label header, vertical list of `HealthRow` items.
- `HealthRow.tsx` (inline within SystemHealthCard) — props: `label: string`, `status: 'healthy' | 'degraded' | 'down' | 'unknown'`, `detail?: string` (e.g. "2m ago" / "13 ms"). Renders: status dot + label + right-aligned detail.
- `useSystemHealth()` — `useQuery` against `GET /api/system/health` with `refetchInterval: 30_000`.
- `useTodayDecisionsCount()` — derives count from `useDecisions()` (no extra fetch); filters by `created_at` falling within today (local TZ).

**Modify:**
- `QuickStats.tsx` — accept new props OR internally derive `decisionsToday` from `useTodayDecisionsCount()`. Wrap each `StatCell` in `<Link>`. Add hover bg + arrow icon.
- `DashboardPage.tsx` — import & render `<SystemHealthCard />`. Remove the `decisionsToday={null}` placeholder; wire real value via the new hook.

**Reuse:** `EmptyState`, `LoadingSkeleton`, `useApiPing` (existing).

---

## 4. State / data shape

### `useSystemHealth`

```ts
export interface SystemHealth {
  api: 'healthy' | 'degraded' | 'down'
  db: 'healthy' | 'degraded' | 'down'
  last_sync_at: string | null      // RFC3339; null = never
  queue_depth?: number             // optional
  checked_at: string               // RFC3339
}

export function useSystemHealth() {
  return useQuery<SystemHealth>({
    queryKey: ['system', 'health'],
    queryFn: () => apiFetch<SystemHealth>('/api/system/health'),
    refetchInterval: 30_000,    // poll every 30s
    staleTime: 20_000,
  })
}
```

### `useTodayDecisionsCount`

```ts
export function useTodayDecisionsCount(): number | null {
  const { data, isLoading } = useDecisions()
  if (isLoading || !data) return null
  const startOfToday = new Date(); startOfToday.setHours(0, 0, 0, 0)
  return data.filter((d) => new Date(d.created_at) >= startOfToday).length
}
```

### GtdPage `?task=ID` handling (light wiring)

```ts
const [params] = useSearchParams()
const focusedTaskId = params.get('task')
// Pass to TaskList / TaskCard so the matched card auto-expands and scrolls into view
useEffect(() => {
  if (!focusedTaskId) return
  const el = document.getElementById(`task-${focusedTaskId}`)
  el?.scrollIntoView({ block: 'center', behavior: 'smooth' })
  el?.classList.add('highlight-pulse')
  const t = setTimeout(() => el?.classList.remove('highlight-pulse'), 1500)
  return () => clearTimeout(t)
}, [focusedTaskId])
```

Add a `.highlight-pulse` keyframe in `index.css` (background tween between `bg-card` and `bg-hover`).

---

## 5. Edge cases

| State | Render |
|-------|--------|
| `useSystemHealth` loading | `LoadingSkeleton h-32 w-full` inside the card slot. Section label still shown. |
| `useSystemHealth` error | Card body shows: red dot + "Unable to fetch health · retrying"; auto-retry every 30s (TanStack Query). |
| `health.api === 'down'` | Row dot red `var(--color-error)`; right detail "down"; overall pill at card top reads "Degraded" red. |
| `health.last_sync_at === null` | Row detail "never" (muted). |
| `useTodayDecisionsCount() === null` (loading) | StatCell shows "—" (existing pattern). |
| `useTodayDecisionsCount() === 0` | StatCell shows "0" — NOT "—". The fix must distinguish "no data yet" from "zero today". |
| `pendingTasks === null` | StatCell shows "—"; cell still clickable Link (still navigates to `/gtd`). |
| GtdPage opened with `?task=invalid-id` | No matching DOM node; no scroll, no highlight; URL retains the param (no error toast — silent no-op). |
| GtdPage opened with `?task=ID` while tasks still loading | After load, the effect re-runs once data is in DOM (use `tasks.length` as effect dep). |

---

## 6. Backend contract

### ⚠️ NEW endpoint: `GET /api/system/health`

- **Method:** GET
- **Path:** `/api/system/health`
- **Auth:** same as other `/api/*` (X-API-Key header)
- **Response 200:**
  ```json
  {
    "api": "healthy",
    "db": "healthy",
    "last_sync_at": "2026-04-28T14:30:00Z",
    "queue_depth": 0,
    "checked_at": "2026-04-28T14:32:07Z"
  }
  ```
- **Response 200 with degraded:** any of `api`/`db` may be `"degraded"` or `"down"`. Endpoint always returns 200 (so frontend `useQuery` doesn't error-spin); status is conveyed inside the body.
- **Response 5xx:** treat as unknown — UI shows "Unable to fetch health" but does NOT crash.

**Backend implementation hint** (out of frontend scope, included for context): pingdb via `db.PingContext(ctx)`; the existing `/health` endpoint in `cmd/server/main.go:115` is unauthenticated and minimal — this new `/api/system/health` is the authenticated, richer version for the dashboard.

### Existing endpoints (no change)

| Endpoint | Hook | Use |
|----------|------|-----|
| `GET /api/decisions` | `useDecisions` | derive today's count client-side |
| `GET /api/context/today` | `useContextToday` | unchanged |

---

## 7. Tailwind class palette

| Purpose | Class / token |
|---------|--------------|
| StatCell (clickable) | `flex-1 rounded-lg p-4 cursor-pointer transition-colors` + `bg-[var(--color-bg-card)]` + `border border-[var(--color-border)]` + `hover:bg-[var(--color-bg-hover)]` |
| StatCell number | `text-2xl font-semibold font-mono` + `var(--color-accent-blue)` |
| StatCell arrow icon | `lucide ArrowRight size={14}` + `var(--color-text-muted)` (positioned top-right) |
| SystemHealthCard wrapper | `rounded-lg p-4` + `bg-[var(--color-bg-card)]` + `border border-[var(--color-border)]` |
| HealthRow | `flex items-center gap-2 py-1.5` + `border-b border-[var(--color-border)] last:border-b-0` |
| HealthRow dot healthy | `w-2 h-2 rounded-full bg-[var(--color-success)]` |
| HealthRow dot degraded | `bg-[var(--color-warning)]` |
| HealthRow dot down | `bg-[var(--color-error)]` |
| HealthRow label | `text-body flex-1` + `var(--color-text-primary)` |
| HealthRow detail | `text-caption font-mono` + `var(--color-text-muted)` |
| Overall pill (degraded) | `text-label rounded-full px-2 py-0.5` + `bg-[var(--color-status-on-hold-bg)]` + `var(--color-status-on-hold-text)` |
| `.highlight-pulse` keyframe | added to `index.css` — animation: 1.5s ease background fade-in then fade-out |

---

## 8. Accessibility

- StatCell as Link: use `<Link to="/gtd" aria-label="Pending tasks: 12. Click to view tasks">`. Keyboard `Enter` activates (default). Focus ring uses global `*:focus-visible` rule.
- SystemHealthCard region: `<section aria-label="System health">`; auto-refresh announces on change via `aria-live="polite"` on the overall pill (announces only when status changes, not every poll).
- HealthRow status dot: NOT colour-only — pair the dot with text label "healthy" / "degraded" / "down" so screen readers and colour-blind users get the same info.
- Loading skeleton: `aria-busy="true"`.
- `/gtd?task=<id>` highlight pulse: respect `prefers-reduced-motion` — when set, skip animation but still scrollIntoView with `behavior: 'auto'`.
- Stat cells navigation: keep them in tab order between QuickStats and SystemHealthCard; do not place them inside another `<button>` (no nested interactive).

---

## 9. Acceptance criteria

- [ ] Dashboard "Decisions today" stat shows the actual count (≥ 0), no longer hard-coded `null`. When loading shows `—`; when zero shows `0`.
- [ ] Both QuickStats cells (`Pending tasks`, `Decisions today`) are wrapped in `<Link>` and navigate respectively to `/gtd` and `/decisions?from=<TODAY>&to=<TODAY>`.
- [ ] Stat cells show a hover background change and a small `ArrowRight` icon; cursor changes to pointer.
- [ ] `SystemHealthCard` renders below QuickStats with at minimum: API status row + DB status row + Last sync row.
- [ ] `useSystemHealth()` calls ⚠️ NEW `GET /api/system/health` and refetches every 30s (TanStack Query `refetchInterval`).
- [ ] When `/api/system/health` returns degraded/down, the corresponding row dot turns amber/red AND the row text reads "degraded"/"down".
- [ ] When `/api/system/health` errors, card shows "Unable to fetch health" but does not crash; auto-retries.
- [ ] GtdPage reads `?task=<id>` from URL; matching `TaskCard` scrolls into view smoothly and pulses 1.5s highlight; no error if id not found.
- [ ] `prefers-reduced-motion: reduce` skips pulse animation but still scrolls.
- [ ] Mobile (< 640 px): SystemHealthCard appears in the right column stack, full-width on mobile, no horizontal overflow.
- [ ] All status visual cues (colour) are paired with text labels (no colour-only signalling).
- [ ] `npm run lint` and `npm run build` exit clean.
