# D2 — GTD TaskRow → Expandable TaskCard with Importance Badge & Project Decisions

**Task:** `cb40ed33`
**File touched:** `web/src/components/gtd/TaskRow.tsx` (rename → `TaskCard.tsx`), `web/src/components/gtd/TaskList.tsx`, `web/src/hooks/useDecisions.ts`
**New components:** `ImportanceBadge.tsx`, `RecentProjectDecisions.tsx`

---

## 1. User journey

1. User opens `/gtd` → sees the existing list of tasks. Each row now shows a small **coloured Importance badge** (1=red 🔴, 2=yellow 🟡, 3=green 🟢) next to the priority dot.
2. User clicks any task row → row **expands in-place** (accordion, no navigation) revealing:
   - Full **description** (no truncation),
   - **Context** (parent project info: project title + area),
   - **Same-project recent decisions** (last 3 decisions matching `task.project_id`, newest first).
3. Clicking the row again — or another row — collapses it (only one expanded at a time, like DashboardPage's ProjectCard).
4. User can still tick the checkbox to complete the task; clicking the checkbox **does not** trigger expand/collapse.
5. Keyboard: `Enter` / `Space` on a focused row toggles expansion; `Tab` moves to the checkbox first, then to the row toggle.

---

## 2. Layout sketch

### Desktop (≥ 1024 px)

```
┌──────────────────────────────────────────────────────────────────────┐
│ Tasks                                                                │
├──────────────────────────────────────────────────────────────────────┤
│ ☐  ●  [🔴]  Refactor handler tests                       Apr 30  ▾  │  ← collapsed
│ ☐  ●  [🟡]  Wire SystemHealthCard widget                 May 02  ▾  │
│ ─────────────────────────────────────────────────────────────────────│
│ ☐  ●  [🟢]  Update README quickstart                     ─        ▴  │  ← expanded
│   ┌──────────────────────────────────────────────────────────────┐  │
│   │ Description                                                  │  │
│   │   Long-form task description here, fully visible, supports   │  │
│   │   multiline content and inline `code`.                       │  │
│   │                                                              │  │
│   │ Context: wayneblacktea · Personal OS                         │  │
│   │                                                              │  │
│   │ Recent decisions in this project (3)                         │  │
│   │ • 2026-04-28  Decisions UI = picker + 3-month default       │  │
│   │ • 2026-04-26  Workspace ProjectDetailPage core interface    │  │
│   │ • 2026-04-21  Notion = mobile briefing only                 │  │
│   └──────────────────────────────────────────────────────────────┘  │
└──────────────────────────────────────────────────────────────────────┘
```

Importance badge is a small coloured pill (12 × 16 px) with a single digit (1 / 2 / 3), placed between PriorityDot and the title.

### Mobile (< 640 px)

```
┌─────────────────────────────┐
│ Tasks                       │
├─────────────────────────────┤
│ ☐ ● [🔴] Refactor han…  ▾  │
│ ☐ ● [🟡] Wire System…   ▾  │
│ ─────────────────────────── │
│ ☐ ● [🟢] Update README   ▴  │
│ ┌─────────────────────────┐ │
│ │ Description             │ │
│ │ Long text wraps here…   │ │
│ │                         │ │
│ │ Context:                │ │
│ │ wayneblacktea · OS      │ │
│ │                         │ │
│ │ Recent decisions (3)    │ │
│ │ • 2026-04-28 Decisions… │ │
│ │ • 2026-04-26 Workspace…│ │
│ └─────────────────────────┘ │
└─────────────────────────────┘
```

Same accordion behaviour on mobile; expanded panel uses full row width.

---

## 3. Component tree

```
TaskList
└── ul
    └── TaskCard (was TaskRow)              ← MODIFY: collapsed/expanded variants
        ├── checkbox (existing, blocks bubble)
        ├── PriorityDot (reuse)
        ├── ImportanceBadge                 ← NEW
        ├── title
        ├── due_date label
        ├── StatusBadge (reuse)
        ├── ChevronDown icon (rotates)
        └── (when expanded) ExpandedPanel   ← NEW inline section
            ├── description block
            ├── ContextLine                 ← NEW (project title + area)
            └── RecentProjectDecisions      ← NEW (max 3 decisions filtered by project_id)
                └── decision row × 3
```

**New:**
- `ImportanceBadge.tsx` — props: `level: 1 | 2 | 3`. Maps 1→red `var(--color-error)`, 2→amber `var(--color-warning)`, 3→green `var(--color-success)`. Renders `<span>` 16×16 px circle with white digit.
- `RecentProjectDecisions.tsx` — props: `projectId: string`. Internally calls `useDecisions()` and filters/slices client-side.
- `ContextLine.tsx` (or inline) — props: `project: Project | undefined`. Renders `{project.title} · {project.area}` or `—` if no parent.

**Modify:**
- Rename `TaskRow.tsx` → `TaskCard.tsx` (file rename + import updates in `TaskList.tsx`). Add `expanded: boolean` and `onToggle: () => void` props.
- `TaskList.tsx` — track `expandedTaskId: string | null`; same single-open pattern as `DashboardPage.tsx`.

**Reuse:** `PriorityDot`, `StatusBadge`, `LoadingSkeleton`, `EmptyState`, existing checkbox markup.

---

## 4. State / data shape

**Importance derivation (no schema change):**
The existing `Task.priority: 1 | 2 | 3 | 4 | 5` field is the source. Map to `importance: 1 | 2 | 3` like so:
```ts
function toImportance(priority: 1|2|3|4|5): 1 | 2 | 3 {
  if (priority >= 4) return 1   // red — highest importance
  if (priority === 3) return 2  // amber
  return 3                      // green — lowest
}
```
Done client-side; no API change. (If product later wants a separate `importance` field, that's a backend follow-up.)

**TaskList local state:**
```ts
const [expandedTaskId, setExpandedTaskId] = useState<string | null>(null)
```

**Hooks used inside `RecentProjectDecisions`:**
```ts
const { data: decisions } = useDecisions()  // existing — TanStack Query, cached
const recent = useMemo(() =>
  (decisions ?? [])
    .filter((d) => d.project_id === projectId)
    .sort((a, b) => b.created_at.localeCompare(a.created_at))
    .slice(0, 3),
  [decisions, projectId]
)
```

**Project lookup:**
The parent `TaskList` already has `projects: Project[]` prop. Pass `project = projects.find(p => p.id === task.project_id)` into TaskCard so children don't refetch.

---

## 5. Edge cases

| State | Render |
|-------|--------|
| Task has no `project_id` (orphan task) | ImportanceBadge still shows. Expanded panel shows description only; ContextLine: "Unassigned" (grey muted); RecentProjectDecisions section omitted entirely. |
| Task has `project_id` but project not found in current projects array | ContextLine: "Project not found" (`text-warning`); RecentProjectDecisions still attempts via decision filter (works because filter is by ID, not object). |
| Description is empty | Expanded panel shows `<EmptyState />`-style muted "No description" line. |
| Decisions still loading inside expanded panel | Show 2 × `LoadingSkeleton h-12` in the decisions section. |
| Decisions errored | Inline muted text: `error.loadFailed`; do NOT block the rest of the panel. |
| `recent.length === 0` (project exists but no decisions) | Muted line: "No decisions logged for this project yet". |
| Task is `done` | Importance badge dims (`opacity: 0.5`); strike-through stays on title. Card still expandable. |
| Two cards expanded simultaneously | NOT allowed — set `expandedTaskId` always replaces, like DashboardPage. |

---

## 6. Backend contract

**No new endpoint.** All data comes from existing hooks:

| Hook | Endpoint | Use |
|------|----------|-----|
| `useTasksByProject(projectId)` | `GET /api/projects/:id/tasks` | already used by TaskList |
| `useProjects()` | `GET /api/projects` | parent → context line lookup |
| `useDecisions()` | `GET /api/decisions` | filtered client-side by `project_id` for "Recent decisions" |

`Decision.project_id` is already on the response (see `web/src/types/api.ts:60`).

**Caveat:** if `useDecisions` returns 1k+ rows, client-side filter is still O(n) per task expansion — fine for MVP. If perf becomes an issue, add `?project_id=xxx&limit=3` server param later.

---

## 7. Tailwind class palette

| Purpose | Class / token |
|---------|--------------|
| Card collapsed (was `<li>` border-bottom) | `flex items-center gap-3 py-3 px-4 min-h-[44px] cursor-pointer` + `border-b var(--color-border)` |
| Card hover bg | `var(--color-bg-hover)` (existing pattern) |
| Importance badge wrapper | `inline-flex items-center justify-center w-4 h-4 rounded-full text-[10px] font-mono font-bold` |
| Importance 1 (red) | `bg-[var(--color-error)] text-white` |
| Importance 2 (amber) | `bg-[var(--color-warning)] text-[var(--color-bg-base)]` |
| Importance 3 (green) | `bg-[var(--color-success)] text-white` |
| Chevron icon | `lucide-react ChevronDown size={14}` rotated 180° when expanded; `transition-transform duration-200` |
| Expanded panel | `px-4 pb-4 pt-2 grid gap-3` + `bg-[var(--color-bg-card)]` (slight indent: `ml-12`) |
| Description text | `text-body whitespace-pre-wrap` + `var(--color-text-primary)` |
| Section label | `text-label` + `var(--color-text-muted)` |
| Recent decisions list | `space-y-1.5 text-body-sm` |
| Decision row | `flex gap-2 items-baseline` + `text-caption font-mono` (date) + `text-body-sm` (title, truncate) |

---

## 8. Accessibility

- Card root: `<li role="group">`. The toggle target is a `<button>` (or `role="button"` on a `div` with `tabIndex={0}` + `aria-expanded={isExpanded}` + `aria-controls={panelId}` + `Enter`/`Space` handler), so screen readers announce expand/collapse state.
- Checkbox: `<input type="checkbox">` already exists; add `onClick={(e) => e.stopPropagation()}` to prevent the parent toggle from firing when ticking.
- ImportanceBadge: `aria-label="Importance ${level} of 3"` on the wrapping span; digit visually shown but `aria-hidden` on the text node to avoid double-read.
- Expanded panel: `<section id={panelId} aria-labelledby={titleId}>`; first focusable element on expand stays the toggle (don't auto-focus the panel — it'd disorient screen-reader users).
- Recent decisions: `<ul aria-label="Recent decisions in this project">`; each `<li>` is read as text, no nested interactive elements.
- Reduced motion: rotate animation on chevron → wrap in `motion-safe:transition-transform` (Tailwind built-in variant).
- Colour: importance colours MUST not be the only signal — show the digit (1/2/3) inside the badge so colour-blind users can still distinguish.

---

## 9. Acceptance criteria

- [ ] `TaskRow` renamed to `TaskCard`; all imports in `TaskList.tsx` (and any other importer) updated; no compile error.
- [ ] Each card renders an `ImportanceBadge` between `PriorityDot` and title, derived from `task.priority` via `toImportance()`.
- [ ] Importance badge shows digit + colour: 1=red, 2=amber, 3=green; passes contrast ≥ 4.5:1 on the badge background.
- [ ] Clicking anywhere on the card body (excluding checkbox) toggles expansion; only one card expanded at a time.
- [ ] Expanded panel shows `description` (or "No description" if empty), `ContextLine` (project title + area or "Unassigned"), and `RecentProjectDecisions` (last 3 by `created_at` desc, filtered by `task.project_id`).
- [ ] Clicking checkbox does NOT toggle expansion (event.stopPropagation works).
- [ ] Keyboard: `Tab` reaches checkbox first then card; `Enter`/`Space` on focused card toggles; `Esc` collapses (optional but tested).
- [ ] `aria-expanded` and `aria-controls` correctly wired; screen reader announces "expanded"/"collapsed".
- [ ] `RecentProjectDecisions` shows skeleton while loading, "No decisions logged for this project yet" when empty, error inline when failed.
- [ ] Mobile (< 640 px): card expands inline, panel takes full width, no horizontal overflow.
- [ ] `npm run lint` and `npm run build` exit clean.
