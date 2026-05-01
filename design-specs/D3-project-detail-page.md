# D3 — Workspace RepoCard → Link → New ProjectDetailPage

**Task:** `c4e533aa`
**Decision references:** `c22d80c2` (Workspace ProjectDetailPage = Claude-repositioned core interface; high information density, copyable selectable text)
**Files touched:** `web/src/components/workspace/RepoCard.tsx`, `web/src/App.tsx`
**New files:** `web/src/pages/ProjectDetailPage.tsx`, `web/src/hooks/useProject.ts`, `web/src/components/workspace/ProjectMetadataPanel.tsx`, `web/src/components/workspace/ProjectActivityPanel.tsx`

---

## 1. User journey

1. User opens `/workspace` → sees the existing 3-column RepoCard grid (no visual change).
2. User notices the cursor changes to pointer over the entire card; clicks any RepoCard.
3. Browser navigates to `/workspace/:projectId` → new ProjectDetailPage loads (1 round-trip to `/api/projects/:id`).
4. Page presents (left ⇄ right two-column on desktop):
   - **Left:** Description, status, language, branch, known_issues, next_planned_step.
   - **Right:** Recent decisions (last 5, project-scoped, with timestamp), in-progress tasks, last_activity timestamp.
5. Every text block is **selectable + copyable** (no hover-to-copy fluff — Cmd/Ctrl+C just works because content is real text in `<pre>` / `<p>`).
6. User clicks "← Back" (top-left) or browser back → returns to `/workspace`.

**Card-level interaction guarantee:** the entire card is the click target. The expand-issues `<button>` (existing inside RepoCard) MUST `e.stopPropagation()` so clicking the chevron doesn't navigate.

---

## 2. Layout sketch

### Desktop (≥ 1024 px, max-width 1200 px)

```
┌──────────────────────────────────────────────────────────────────────┐
│ ← Back to Workspace                                                  │
│                                                                      │
│ [Go]  wayneblacktea                                  ● active        │
│       Personal OS for tracking decisions and projects                │
│       feature/p0-conversational-triggers                             │
├──────────────────────────────────────────────────────────────────────┤
│ ┌────────────────────────────┐ ┌────────────────────────────────────┐ │
│ │ DESCRIPTION                │ │ RECENT DECISIONS (5)               │ │
│ │ Long-form description …    │ │ • 2026-04-28 14:32  Decisions UI…  │ │
│ │ supports paragraph breaks  │ │ • 2026-04-26 09:15  Workspace…    │ │
│ │                            │ │ • 2026-04-21 18:02  Notion…       │ │
│ │ STATUS         active      │ │ • 2026-04-19 11:40  …             │ │
│ │ LANGUAGE       Go          │ │ • 2026-04-15 08:30  …             │ │
│ │ BRANCH         feature/…   │ │                                    │ │
│ │                            │ │ NEXT PLANNED STEP                  │ │
│ │ KNOWN ISSUES (2)           │ │ Wire SystemHealthCard widget       │ │
│ │ • SQL migration drift      │ │                                    │ │
│ │ • Concept proposal flake   │ │ IN-PROGRESS TASKS (3)              │ │
│ │                            │ │ ● [🔴] Refactor handler tests      │ │
│ │                            │ │ ● [🟡] Wire SystemHealthCard…      │ │
│ │                            │ │ ● [🟢] Update README quickstart    │ │
│ │                            │ │                                    │ │
│ │                            │ │ LAST ACTIVITY                      │ │
│ │                            │ │ 2026-04-28 14:32:07                │ │
│ └────────────────────────────┘ └────────────────────────────────────┘ │
└──────────────────────────────────────────────────────────────────────┘
```

### Mobile (< 640 px)

```
┌─────────────────────────────┐
│ ← Back                      │
│                             │
│ [Go] wayneblacktea  ● active│
│ Personal OS for…            │
│ feature/p0-conversational…  │
├─────────────────────────────┤
│ DESCRIPTION                 │
│ Long form…                  │
│                             │
│ STATUS:    active           │
│ LANGUAGE:  Go               │
│ BRANCH:    feature/…        │
│                             │
│ KNOWN ISSUES (2)            │
│ • SQL migration drift       │
│ • Concept proposal flake    │
├─────────────────────────────┤
│ RECENT DECISIONS (5)        │
│ • 2026-04-28 14:32  …       │
│ • 2026-04-26 09:15  …       │
│ …                           │
│                             │
│ NEXT PLANNED STEP           │
│ Wire SystemHealthCard…      │
│                             │
│ IN-PROGRESS TASKS (3)       │
│ ● [🔴] Refactor handler…    │
│ ● [🟡] Wire SystemHealthCard│
│ ● [🟢] Update README…       │
│                             │
│ LAST ACTIVITY               │
│ 2026-04-28 14:32:07         │
└─────────────────────────────┘
```

Single column on mobile; left panel content stacks above right panel content.

---

## 3. Component tree

```
ProjectDetailPage
├── BackLink                       ← NEW (lucide ArrowLeft + react-router Link to /workspace)
├── ProjectHeader                  ← NEW (LanguageBadge + name + status dot + description + branch)
├── grid lg:grid-cols-2
│   ├── ProjectMetadataPanel       ← NEW
│   │   ├── DescriptionBlock (full text, whitespace-pre-wrap)
│   │   ├── KeyValueRow ×3 (Status / Language / Branch)
│   │   └── KnownIssuesList (reuse styling from RepoCard)
│   └── ProjectActivityPanel       ← NEW
│       ├── RecentDecisionsList    ← NEW (last 5 decisions, scoped to project_id OR repo_name)
│       ├── NextPlannedStepBlock
│       ├── InProgressTasksList    ← NEW (filters tasks where status === 'in_progress')
│       └── LastActivityLine
└── (loading / error / not-found states wrap the whole page)
```

**New components:**
- `ProjectDetailPage.tsx` — page-level shell, reads `:projectId` from `useParams()`.
- `ProjectMetadataPanel.tsx` — pure presentational, props: `repo: Repo`.
- `ProjectActivityPanel.tsx` — pure presentational, props: `decisions: Decision[]`, `tasks: Task[]`, `lastActivity: string | null`, `nextPlannedStep: string | null`.
- `RecentDecisionsList.tsx` — props: `decisions: Decision[]`, `limit = 5`. Each row: `<time>` + `title` (truncate, click → navigate to `/decisions?focus=<id>` — optional, can skip for MVP).
- `InProgressTasksList.tsx` — props: `tasks: Task[]`. Reuses `PriorityDot` + `ImportanceBadge` (from D2). Each row clickable → navigate to `/gtd?task=<id>` (D4 wires this).
- `useProject(projectId).ts` — `useQuery<Repo>` against `GET /api/projects/:id`. **NOTE:** the existing `Repo` type already has all the fields needed (`description`, `status`, `language`, `current_branch`, `known_issues`, `next_planned_step`, `last_activity`). If backend uses `Project` (different shape), see §6.

**Reuse:** `LoadingSkeleton`, `EmptyState`, `LanguageBadge` (extract from inside `RepoCard.tsx` into `web/src/components/workspace/LanguageBadge.tsx`), `PriorityDot`, `StatusBadge`.

**Modified:**
- `RepoCard.tsx` — wrap entire `<article>` in `<Link to={\`/workspace/${repo.id}\`}>`. The expand-issues button MUST stay clickable inline; add `e.stopPropagation()` + `e.preventDefault()` on its `onClick`. Cursor remains pointer (already implicit via Link).
- `App.tsx` — add route `{ path: 'workspace/:projectId', element: <ProjectDetailPage /> }`.

---

## 4. State / data shape

```ts
// New hook
export function useProject(projectId: string) {
  return useQuery<Repo>({
    queryKey: ['workspace', 'repos', projectId],
    queryFn: () => apiFetch<Repo>(`/api/workspace/repos/${projectId}`),
    enabled: Boolean(projectId),
  })
}

// ProjectDetailPage internals
const { projectId = '' } = useParams<{ projectId: string }>()
const { data: repo, isLoading, isError } = useProject(projectId)
const { data: decisions = [] } = useDecisions()       // existing — full list, filter client-side
const { data: tasks = [] } = useTasksByProject(projectId)  // existing

const projectDecisions = useMemo(() =>
  decisions
    .filter((d) => d.project_id === projectId || d.repo_name === repo?.name)
    .sort((a, b) => b.created_at.localeCompare(a.created_at))
    .slice(0, 5),
  [decisions, projectId, repo?.name]
)

const inProgressTasks = useMemo(() =>
  tasks.filter((t) => t.status === 'in_progress'),
  [tasks]
)
```

**TanStack Query** chosen throughout (consistent with the rest of the codebase). No Zustand needed — there's no cross-component shared state for this page.

---

## 5. Edge cases

| State | Render |
|-------|--------|
| `isLoading` | Full-page skeleton: header `LoadingSkeleton h-16`, then 2-col grid with 4 × `LoadingSkeleton h-32`. |
| `isError` (network) | Centered error block: title "Could not load project"; "Try again" button calls `refetch()`; secondary "Back to Workspace" link. |
| Repo not found (HTTP 404) | Empty state: `<EmptyState messageKey="workspace.projectNotFound" ctaLabelKey="workspace.backToWorkspace" onCta={navigateBack} />`. |
| `repo.description` null | Skip the DescriptionBlock entirely (or show muted "No description"). |
| `repo.known_issues` null/empty | Skip the KnownIssuesList section entirely. |
| `repo.last_activity` null | "—" |
| `repo.next_planned_step` null | Skip section. |
| Decisions empty for this project | Section header still shown; body: muted "No decisions logged yet". |
| `inProgressTasks.length === 0` | Section header still shown; body: muted "No tasks in progress". |
| User pastes invalid `:projectId` (non-UUID) | Backend returns 400/404; treat same as "not found". |
| User has no permission (future) | NOT in MVP scope (no auth gate currently). |

---

## 6. Backend contract

### Existing endpoint (verify shape matches `Repo` type)

`GET /api/workspace/repos/:id` — **likely needs to be added.** The current `useRepos()` calls `GET /api/workspace/repos` returning `Repo[]`; per-repo `GET /api/workspace/repos/:id` may not yet exist.

⚠️ **NEW endpoint:** `GET /api/workspace/repos/:id`
- **Method:** GET
- **Path:** `/api/workspace/repos/:id` (UUID path param)
- **Response 200:** `Repo` (existing TS type — no new fields needed)
- **Response 404:** `{ "error": "repo not found" }`

**Fallback if backend resists adding this:** call `useRepos()` and `.find(r => r.id === projectId)` client-side. Trade-off: extra payload (~3 kB) but zero backend work. **Recommend the new endpoint for cleanliness**, fallback acceptable for MVP.

### Existing endpoints (no change)

| Endpoint | Used by |
|----------|---------|
| `GET /api/decisions` | `useDecisions()` — filter by `project_id` and/or `repo_name` client-side |
| `GET /api/projects/:id/tasks` | `useTasksByProject()` — filter by `status === 'in_progress'` client-side |

### Note on `repo` vs `project` model

The current TS `Repo` type and the `Project` type are **different concepts** here:
- `Project` (from `useProjects`) is the GTD-domain entity (with `goal_id`, `area`, `priority`).
- `Repo` (from `useRepos`) is the workspace-domain entity (with `language`, `branch`, `known_issues`, `next_planned_step`, `last_activity`).

The route param `:projectId` for THIS page is a **Repo ID** (workspace UUID), not a GTD Project ID. Frontend-engineer should use the term `repoId` internally to avoid confusion, but the URL `/workspace/:projectId` reads naturally to users (a "project" in their mental model is a repo). Document this in a code comment.

---

## 7. Tailwind class palette

| Purpose | Class / token |
|---------|--------------|
| Page wrapper | `p-6 max-w-[1200px] mx-auto` |
| Back link | `inline-flex items-center gap-1 text-body-sm mb-4` + `var(--color-accent-blue)` + `hover:underline` |
| Header row | `flex items-start gap-3 mb-2` |
| Project name | `text-page-title font-mono` + `var(--color-text-primary)` |
| Status dot active | `w-2 h-2 rounded-full bg-[var(--color-success)]` |
| Status dot non-active | `w-2 h-2 rounded-full bg-[var(--color-text-muted)]` |
| Branch label | `font-mono text-caption` + `var(--color-text-muted)` |
| Two-col grid | `grid grid-cols-1 lg:grid-cols-2 gap-6 mt-6` |
| Panel container | `rounded-lg p-5` + `bg-[var(--color-bg-card)]` + `border border-[var(--color-border)]` |
| Section label (UPPERCASE) | `text-label mb-2` + `var(--color-text-muted)` |
| KeyValueRow | `flex items-baseline gap-2 mb-1 text-body-sm` |
| Description block | `text-body whitespace-pre-wrap select-text` + `var(--color-text-primary)` |
| Decision row | `flex gap-2 items-baseline py-1 text-body-sm` |
| Decision timestamp | `font-mono text-caption shrink-0 w-[140px]` + `var(--color-text-muted)` |
| Task row | `flex items-center gap-2 py-1 text-body-sm cursor-pointer hover:bg-[var(--color-bg-hover)]` |
| Last activity | `font-mono text-caption` + `var(--color-text-muted)` |

**Selectable text:** add `select-text` (Tailwind utility) on description/decisions/issues blocks. Body text already selectable by default — explicit utility just signals intent.

---

## 8. Accessibility

- Card-as-link: `<Link to={...}>` wraps the whole card. **Use `<Link>` not nested `<button>` inside `<a>`** to keep HTML valid. The expand-issues control inside RepoCard becomes a `<button>` sibling at the bottom; it stops propagation.
- BackLink: `<Link aria-label="Back to workspace">`; visible label "← Back to Workspace".
- Page title: `<h1>` is the project name; status dot has `aria-label="Status: ${status}"`.
- Section labels: use `<h2 className="text-label">` semantically — assistive tech navigates by headings.
- Recent decisions: `<ol>` with `<time dateTime={d.created_at}>`. Each item is plain text by default; if links to `/decisions?focus=<id>` are added, they're keyboard-reachable.
- In-progress tasks: each row is a clickable element → use `<Link to={\`/gtd?task=${t.id}\`}>` (better than `<div onClick>` — keyboard + screen reader friendly out of the box).
- Loading state: `aria-busy="true"` on main container; skeleton has no text content.
- Error state: `role="alert"` on error block.
- Focus order: BackLink → first interactive element in left panel → first interactive in right panel.
- Preserve scroll: when navigating from `/workspace` → detail → back, restore scroll position (react-router-dom v7 default behaviour, verify).

---

## 9. Acceptance criteria

- [ ] `RepoCard` is wrapped in `<Link to={\`/workspace/${repo.id}\`}>`; clicking anywhere on the card navigates.
- [ ] The "known issues" expand chevron inside RepoCard still works without navigating (uses `e.stopPropagation()` + `e.preventDefault()`).
- [ ] `App.tsx` declares route `workspace/:projectId` → `<ProjectDetailPage />`.
- [ ] `useProject(projectId)` hook calls `GET /api/workspace/repos/:id` (or fallback to client-side `.find()` from `useRepos()` cache); returns typed `Repo`.
- [ ] Page renders all of: description, status, language, current_branch, known_issues, next_planned_step, last_activity (or omits cleanly when null).
- [ ] Recent decisions panel shows up to 5 entries filtered by `decision.project_id === projectId || decision.repo_name === repo.name`, sorted newest first, with `YYYY-MM-DD HH:MM` timestamp + title.
- [ ] In-progress tasks panel shows tasks where `status === 'in_progress'`, each with `PriorityDot` + `ImportanceBadge` + clickable Link to `/gtd?task=<id>`.
- [ ] All text content is selectable with mouse and copyable via Cmd/Ctrl+C (no JS interception).
- [ ] Loading state: skeleton placeholders for header + both panels.
- [ ] Error state: friendly retry UI; 404 shows EmptyState with "Back to Workspace" CTA.
- [ ] Mobile (< 640 px): single-column layout, no horizontal scroll, all sections accessible.
- [ ] Browser back from detail → workspace restores scroll position.
- [ ] `npm run lint` and `npm run build` exit clean.
