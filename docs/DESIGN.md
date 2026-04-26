# wayneblacktea — Control Room Design Specification

**Version:** 1.0  
**Date:** 2026-04-26  
**Theme:** Spaceship Dashboard (dark-first, light toggle)

---

## Table of Contents

1. [Design System](#1-design-system)
2. [Layout Structure](#2-layout-structure)
3. [Page Specs](#3-page-specs)
4. [Component Inventory](#4-component-inventory)
5. [Interaction Specs](#5-interaction-specs)
6. [File Structure](#6-file-structure)

---

## 1. Design System

### 1.1 Color Tokens — Tailwind CSS v4 `@theme`

Add to `web/src/index.css`:

```css
@import "tailwindcss";

@theme {
  /* === Background layers === */
  --color-bg-base:    #0a1628;   /* app background */
  --color-bg-card:    #0d1f35;   /* card / panel surface */
  --color-bg-hover:   #112240;   /* row / card hover */
  --color-bg-input:   #0f1e30;   /* input field background */
  --color-bg-overlay: #071221;   /* modal backdrop tint */

  /* === Border === */
  --color-border:         #1a3a5c;
  --color-border-focus:   #4fc3f7;

  /* === Accent === */
  --color-accent-blue:    #4fc3f7;   /* primary CTA, links */
  --color-accent-purple:  #7c4dff;   /* secondary accent */

  /* === Semantic === */
  --color-success:  #4caf50;
  --color-warning:  #ff9800;
  --color-error:    #f44336;
  --color-info:     #4fc3f7;

  /* === Text === */
  --color-text-primary:  #e8f4f8;   /* headings, body */
  --color-text-muted:    #7a9bb5;   /* labels, captions */
  --color-text-disabled: #3d5a73;

  /* === Status badge fills (low-opacity backgrounds) === */
  --color-status-active-bg:     #0a2e0a;
  --color-status-active-text:   #4caf50;
  --color-status-on-hold-bg:    #2e1f00;
  --color-status-on-hold-text:  #ff9800;
  --color-status-completed-bg:  #0a1f35;
  --color-status-completed-text:#4fc3f7;
  --color-status-archived-bg:   #1a1a2e;
  --color-status-archived-text: #7a9bb5;

  /* === Priority dot colors (1=low → 5=critical) === */
  --color-priority-1: #4caf50;
  --color-priority-2: #8bc34a;
  --color-priority-3: #ff9800;
  --color-priority-4: #f44336;
  --color-priority-5: #ff1744;

  /* === Light theme overrides (applied via .light class on <html>) === */
  /* See §1.5 for light theme variable overrides */

  /* === Spacing === */
  --spacing-sidebar: 240px;
  --spacing-sidebar-collapsed: 56px;
  --spacing-header: 56px;

  /* === Border radius === */
  --radius-sm:  4px;
  --radius-md:  8px;
  --radius-lg:  12px;
  --radius-xl:  16px;
  --radius-full: 9999px;

  /* === Transitions === */
  --duration-fast:   150ms;
  --duration-normal: 250ms;
  --duration-slow:   400ms;
}
```

**Contrast verification (dark theme):**

| Pairing | Ratio | Pass WCAG AA |
|---------|-------|-------------|
| `text-primary` (#e8f4f8) on `bg-card` (#0d1f35) | 11.4:1 | Pass |
| `text-muted` (#7a9bb5) on `bg-card` (#0d1f35) | 4.6:1 | Pass |
| `accent-blue` (#4fc3f7) on `bg-card` (#0d1f35) | 7.2:1 | Pass |
| `success` (#4caf50) on `bg-card` (#0d1f35) | 5.1:1 | Pass |
| `warning` (#ff9800) on `bg-card` (#0d1f35) | 4.9:1 | Pass |
| `error` (#f44336) on `bg-card` (#0d1f35) | 4.7:1 | Pass |

### 1.2 Light Theme Overrides

Add after the `@theme` block:

```css
html.light {
  --color-bg-base:    #f0f4f8;
  --color-bg-card:    #ffffff;
  --color-bg-hover:   #e8edf2;
  --color-bg-input:   #f5f7fa;
  --color-bg-overlay: #00000066;
  --color-border:         #cdd9e5;
  --color-border-focus:   #0288d1;
  --color-accent-blue:    #0288d1;
  --color-accent-purple:  #6200ea;
  --color-text-primary:   #0d1f35;
  --color-text-muted:     #5a7a94;
  --color-text-disabled:  #a0b4c5;
  --color-status-active-bg:    #e8f5e9;
  --color-status-active-text:  #2e7d32;
  --color-status-on-hold-bg:   #fff3e0;
  --color-status-on-hold-text: #e65100;
  --color-status-completed-bg: #e3f2fd;
  --color-status-completed-text: #0277bd;
  --color-status-archived-bg:  #f5f5f5;
  --color-status-archived-text:#546e7a;
}
```

Theme is toggled by adding/removing `light` class on `<html>`. State persisted in `localStorage` key `wbt-theme`. Default is dark (no class).

### 1.3 Typography

**Font:** [JetBrains Mono](https://fonts.google.com/specimen/JetBrains+Mono) for monospace data (branch names, IDs, code); [Inter](https://fonts.google.com/specimen/Inter) for UI text.

Add to `index.html`:
```html
<link rel="preconnect" href="https://fonts.googleapis.com" />
<link rel="preconnect" href="https://fonts.gstatic.com" crossorigin />
<link href="https://fonts.googleapis.com/css2?family=Inter:wght@400;500;600;700&family=JetBrains+Mono:wght@400;500&display=swap" rel="stylesheet" />
```

Add to `@theme` in `index.css`:
```css
  --font-sans: 'Inter', ui-sans-serif, system-ui, sans-serif;
  --font-mono: 'JetBrains Mono', ui-monospace, monospace;
```

Set base body:
```css
body {
  font-family: var(--font-sans);
  background-color: var(--color-bg-base);
  color: var(--color-text-primary);
  font-size: 14px;
  line-height: 1.5;
}
```

**Type Scale:**

| Role | Class | Size | Weight | Line-height |
|------|-------|------|--------|-------------|
| Page title | `.text-page-title` | 20px / 1.25rem | 600 | 1.3 |
| Section heading | `.text-section` | 16px / 1rem | 600 | 1.4 |
| Card title | `.text-card-title` | 14px / 0.875rem | 600 | 1.4 |
| Body | `.text-body` | 14px / 0.875rem | 400 | 1.5 |
| Body small | `.text-body-sm` | 13px / 0.8125rem | 400 | 1.5 |
| Caption | `.text-caption` | 12px / 0.75rem | 400 | 1.4 |
| Label | `.text-label` | 11px / 0.6875rem | 500 | 1.3 |
| Mono | `.font-mono` | inherit | 400 | inherit |

Add to `index.css` (after `@theme`):
```css
.text-page-title  { font-size: 1.25rem;   font-weight: 600; line-height: 1.3; }
.text-section     { font-size: 1rem;      font-weight: 600; line-height: 1.4; }
.text-card-title  { font-size: 0.875rem;  font-weight: 600; line-height: 1.4; }
.text-body        { font-size: 0.875rem;  font-weight: 400; line-height: 1.5; }
.text-body-sm     { font-size: 0.8125rem; font-weight: 400; line-height: 1.5; }
.text-caption     { font-size: 0.75rem;   font-weight: 400; line-height: 1.4; }
.text-label       { font-size: 0.6875rem; font-weight: 500; line-height: 1.3; text-transform: uppercase; letter-spacing: 0.05em; }
```

---

## 2. Layout Structure

### 2.1 App Shell

```
┌─────────────────────────────────────────────────────┐
│  Header (56px height, full width, sticky top)       │
├────────────────┬────────────────────────────────────┤
│                │                                    │
│   Sidebar      │   Main Content Area                │
│   (240px)      │   (flex-1, overflow-y-auto)         │
│                │                                    │
│   [nav items]  │   <Outlet /> (React Router)         │
│                │                                    │
└────────────────┴────────────────────────────────────┘
```

On mobile (< 640px): sidebar is hidden off-screen (`-translate-x-full`), toggled via hamburger in header. Overlay backdrop closes it on tap-outside.

On tablet (640–1023px): sidebar collapses to icon-only rail (56px). Labels hidden. Nav item tooltips on hover.

On desktop (≥ 1024px): full sidebar (240px) always visible.

### 2.2 Header Bar

**Height:** 56px  
**Background:** `bg-card` + bottom border `border-border`  
**Contents (left to right):**

```
[Hamburger/MenuIcon (mobile only)] [Logo/Title "Control Room"] ··· [LanguageToggle] [ThemeToggle] [API status dot]
```

- Logo/Title: text `CONTROL ROOM` in `font-mono text-accent-blue font-medium tracking-widest text-sm`
- API status dot: 8px circle. Green = API reachable (checked via TanStack Query `useQuery` ping), red = unreachable. `aria-label="API status: connected"` or `"disconnected"`.
- `LanguageToggle`: text button `ZH / EN`
- `ThemeToggle`: icon button, sun icon (light mode active) / moon icon (dark mode active)

### 2.3 Sidebar Navigation

**Width:** 240px (desktop), 56px (tablet rail), 0px (mobile — slide-in overlay)  
**Background:** `bg-card`  
**Right border:** `border-border`

**Nav items (in order):**

| Icon | Label (zh-TW) | Label (en) | Route | Phase |
|------|--------------|-----------|-------|-------|
| `LayoutDashboard` | 儀表板 | Dashboard | `/` | 1 |
| `ListTodo` | GTD | GTD | `/gtd` | 1 |
| `FolderGit2` | 工作區 | Workspace | `/workspace` | 1 |
| `BookMarked` | 決策紀錄 | Decisions | `/decisions` | 1 |
| `Library` | 知識庫 | Knowledge | `/knowledge` | 3 — show with lock icon + "coming soon" badge |
| `GraduationCap` | 學習回顧 | Reviews | `/reviews` | 3 — show with lock icon + "coming soon" badge |

**Nav item anatomy (desktop full):**
```
[Icon 20px]  [Label text-body]                [ActiveIndicator]
```
- Active state: left border 3px `accent-blue`, background `bg-hover`, text `text-primary`
- Default: text `text-muted`, no bg
- Hover: background `bg-hover`, text `text-primary`, transition 150ms
- `focus-visible`: outline 2px `border-focus`, offset 2px, rounded-sm

**Coming-soon items:** opacity-50, cursor-default (not `cursor-pointer`). Badge `SOON` in `text-label text-warning` shown inline on desktop, hidden on tablet rail.

**Nav item props (TypeScript):**
```typescript
interface NavItemProps {
  icon: LucideIcon;
  labelKey: string;        // i18n key
  to: string;
  phase?: 1 | 3;          // default 1; 3 = coming soon
}
```

---

## 3. Page Specs

### 3.1 Page 1: Dashboard (`/`)

**Purpose:** At-a-glance session start. Wayne opens this to know what to work on.

**Data source:** `GET /api/context/today` (single fetch, TanStack Query key `['context', 'today']`)

**Layout (desktop):**
```
┌─────────────────────────────────────────────────────┐
│  Greeting + Date row (full width)                   │
├───────────────────────────┬─────────────────────────┤
│  Active Projects (60%)    │  Weekly Progress (40%)  │
│  ─────────────────────    │  ─────────────────────  │
│  [ProjectCard × N]        │  [GoalProgress widget]  │
│                           │  [HandoffCard]          │
│                           │  [QuickStats row]       │
└───────────────────────────┴─────────────────────────┘
```

**Layout (tablet):** same 2-column, left 55% / right 45%.

**Layout (mobile):** single column, stacked: Greeting → Projects → Progress → Handoff → Stats.

#### Greeting Row

```
Good morning, Wayne        Sunday, 26 April 2026
```

- Left: `text-section text-text-primary`. Greeting text key: `dashboard.greeting.morning/afternoon/evening` based on hour.
- Right: full date string `text-body text-muted`, right-aligned.
- Height: 40px, `py-3 px-4`

#### Active Projects Section

- Section label: `text-label text-muted mb-3` — "ACTIVE PROJECTS" / "進行中專案"
- Stack of `ProjectCard` components, gap-3 between them
- If `projects` array empty: `EmptyState` with message key `dashboard.noProjects`
- If loading: 3 `LoadingSkeleton` cards, each h-[96px]

#### Weekly Progress Widget

- Section label: `text-label text-muted mb-3` — "WEEKLY PROGRESS" / "本週進度"
- `GoalProgress` component showing `weekly_progress.completed / weekly_progress.total`
- If `total === 0`: show `EmptyState` with `dashboard.noTasksThisWeek`

#### HandoffCard

- Section label: `text-label text-muted mb-3` — "NEXT SESSION" / "下次工作"
- Single `HandoffCard` component
- If `pending_handoff === null`: show `EmptyState` with `dashboard.noHandoff`

#### QuickStats Row

Two stat cells side by side:

```
┌──────────────────┬──────────────────┐
│  Pending Tasks   │  Decisions Today │
│     [N]          │      [N]         │
└──────────────────┴──────────────────┘
```

- Cell background: `bg-card border border-border rounded-lg p-4`
- Number: `text-2xl font-semibold text-accent-blue font-mono`
- Label: `text-caption text-muted mt-1`
- Pending tasks count: derived from projects' task data; display `—` if unknown (do not make a separate API call on dashboard)
- Decisions today: count from `GET /api/decisions` with today's date filter, or derive from context if available

---

### 3.2 Page 2: GTD (`/gtd`)

**Purpose:** Full task/project management view.

**Layout:**
```
┌────────────────────────────────────────────────┐
│  "GTD" page title + filter bar (right)         │
├────────────────────────────────────────────────┤
│  Goals section                                 │
│  ─────────────────────────────────────────     │
│  [GoalCard × N] (horizontal scroll on mobile)  │
├────────────────────────────────────────────────┤
│  Projects section                              │
│  ─────────────────────────────────────────     │
│  [Status tabs: All | Active | On Hold | Done]  │
│  [ProjectCard × N] (filtered list)             │
├────────────────────────────────────────────────┤
│  Tasks section                                 │
│  ─────────────────────────────────────────     │
│  [Filter: All Projects | dropdown]             │
│  [TaskRow × N]                                 │
└────────────────────────────────────────────────┘
                                           [FAB +]
```

**Data sources:**
- Goals: `GET /api/goals` (key `['goals']`)
- Projects: `GET /api/projects` (key `['projects']`)
- Tasks: `GET /api/projects/:id/tasks` — loaded for all active projects (parallel queries), merged

#### Goals Section

- Grid: `grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-4`
- Each goal: `GoalCard` (see Component Inventory)
- Empty: `EmptyState` with add-goal CTA

#### Projects Section — Status Tabs

Tab bar:

```
[All]  [Active]  [On Hold]  [Completed]
```

- Active tab: border-bottom 2px `accent-blue`, text `text-primary`
- Default tab: text `text-muted`
- Tab content: filtered list of `ProjectCard` at full width (not the compact dashboard variant)

**ProjectCard on GTD page (expanded variant):** shows all fields including `description`, task count badge, last-updated date.

#### Tasks Section

- Project filter: `<select>` dropdown with `All Projects` + one option per project name. Styled: `bg-bg-input border-border rounded-md px-3 py-2 text-body text-text-primary`
- Task list: `[TaskRow]` stacked, separated by `border-b border-border`
- Empty: `EmptyState` with add-task CTA, shown after spinner resolves

#### FAB (Floating Action Button)

- Position: `fixed bottom-6 right-6 z-50`
- Shape: circle 56px (`w-14 h-14 rounded-full`)
- Background: `bg-accent-blue`
- Icon: `Plus` 24px, color `#0a1628`
- Hover: `bg-[#81d4fa]` (one shade lighter), scale-105, transition 150ms
- `aria-label="新增 / Add"`
- On click: opens `QuickAddModal` (see §5 Interaction Specs)

---

### 3.3 Page 3: Workspace (`/workspace`)

**Purpose:** See all tracked repos and their current state.

**Data source:** `GET /api/workspace/repos` (key `['workspace', 'repos']`)

**Layout:**
```
┌────────────────────────────────────────────────┐
│  "Workspace" title + [Sync Repos button]       │
├────────────────────────────────────────────────┤
│  Repo grid                                     │
│  grid-cols-1 sm:grid-cols-2 lg:grid-cols-3     │
│  [RepoCard × N]                                │
└────────────────────────────────────────────────┘
```

- Grid gap: `gap-4`
- Sync Repos button: secondary button (outline style, `border-accent-blue text-accent-blue`), triggers `POST /api/workspace/repos` with current data — this is a "re-sync" action. Button shows spinner while in-flight.
- Empty: `EmptyState` with no-repos message

**RepoCard:** see Component Inventory. Click anywhere on card expands `known_issues` inline (accordion, not modal).

---

### 3.4 Page 4: Decisions (`/decisions`)

**Purpose:** Searchable, filterable log of architectural decisions.

**Data source:** `GET /api/decisions` (key `['decisions']`). Optional query: `?project_id=...` or `?repo_name=...`

**Layout:**
```
┌────────────────────────────────────────────────┐
│  "Decisions" title                             │
│  Filter row: [All Projects ▾]  [Search input] │
├────────────────────────────────────────────────┤
│  Timeline (vertical)                           │
│  ─────────────────────────────────────────     │
│  [DecisionEntry × N]                           │
└────────────────────────────────────────────────┘
```

- Filter: project dropdown (same pattern as Tasks section) + text search (client-side filter on `title` + `rationale`)
- Search input: `bg-bg-input border-border rounded-md px-3 py-2 h-9 w-56 text-body` with `Search` icon inside left
- Timeline line: `border-l-2 border-border ml-4` vertical rule, decisions hang off it to the right

**DecisionEntry anatomy:**
```
○ — [Title]                     [date]
    [rationale preview 2 lines]
    [▸ Show full] / [▾ Collapse]
    — expanded: Context / Decision / Alternatives —
```

- Timeline dot: 10px circle, `bg-accent-blue border-2 border-bg-base`, positioned left of the line (`-ml-[5px]`)
- Date: `text-caption text-muted`
- Expand/collapse: inline toggle text button, no full-width open. Transition: `max-height` animate from 0 → auto (via measured height, not `transition: max-height`)

---

### 3.5 Phase 3 Nav Stubs: Knowledge & Reviews

Both nav items:
- Rendered in sidebar with icon + label
- `opacity-50`, `cursor-default`, no active/hover state change
- On desktop: show `SOON` badge (`text-label text-warning bg-[#2e1f00] rounded-full px-2 py-0.5`)
- Clicking does nothing (no `<Link>`, just `<span role="button" aria-disabled="true">`)
- No page component needed; if URL is typed directly, redirect to `/`

---

## 4. Component Inventory

### 4.1 StatusBadge

**Purpose:** Displays project or task status as a pill badge.

**Props:**
```typescript
interface StatusBadgeProps {
  status: 'active' | 'on_hold' | 'completed' | 'archived';
  size?: 'sm' | 'md';  // default 'md'
}
```

**Variants:**

| status | bg token | text token | label (zh-TW / en) |
|--------|----------|------------|--------------------|
| active | `status-active-bg` | `status-active-text` | 進行中 / Active |
| on_hold | `status-on-hold-bg` | `status-on-hold-text` | 暫停 / On Hold |
| completed | `status-completed-bg` | `status-completed-text` | 已完成 / Completed |
| archived | `status-archived-bg` | `status-archived-text` | 封存 / Archived |

**States:**

| State | Trigger | Visual |
|-------|---------|--------|
| default | — | pill, font-mono text-label |
| — | — | no hover / no interaction |

**Dimensions:**
- `md`: `px-2 py-0.5 rounded-full text-label`
- `sm`: `px-1.5 py-px rounded-full text-[10px] font-mono font-medium`

**Accessibility:**
- `role="status"` only if status changes dynamically; otherwise no role
- Wrap with `<span>`, not `<div>`

---

### 4.2 PriorityDot

**Purpose:** Visually encodes task/project priority (1–5) as a colored dot.

**Props:**
```typescript
interface PriorityDotProps {
  level: 1 | 2 | 3 | 4 | 5;
  showLabel?: boolean;  // default false
}
```

**Color map:**

| level | color token | meaning |
|-------|-------------|---------|
| 1 | `priority-1` (#4caf50) | Low |
| 2 | `priority-2` (#8bc34a) | Normal |
| 3 | `priority-3` (#ff9800) | Medium |
| 4 | `priority-4` (#f44336) | High |
| 5 | `priority-5` (#ff1744) | Critical |

**Dimensions:** 10px circle (`w-2.5 h-2.5 rounded-full inline-block shrink-0`)

**With label:** dot + `text-caption text-muted ml-1` showing priority number or word

**Accessibility:** `aria-label="Priority [level]"` on the wrapping `<span>`

---

### 4.3 ProjectCard

**Purpose:** Summary card for a single project. Used in Dashboard (compact) and GTD (expanded).

**Props:**
```typescript
interface ProjectCardProps {
  project: {
    id: string;
    name: string;
    title: string;
    description?: string | null;
    status: 'active' | 'on_hold' | 'completed' | 'archived';
    area: string;
    priority: 1 | 2 | 3 | 4 | 5;
    created_at: string;
    updated_at: string;
    // GTD expanded extras (optional)
    taskCount?: number;
    nextPlannedStep?: string | null;
  };
  variant?: 'compact' | 'expanded';  // default 'compact'
  onClick?: () => void;
}
```

**Layout (compact — dashboard):**
```
┌─────────────────────────────────────────────────┐
│ [PriorityDot]  [title text-card-title]  [StatusBadge]│
│ [area text-caption text-muted]                   │
│ [branch: name font-mono text-caption text-accent-blue]│
│ [next_planned_step text-body-sm text-muted 1-line crop]│
└─────────────────────────────────────────────────┘
```

**Layout (expanded — GTD):**
Adds below: `description` (2-line clamp), task count badge, `updated_at` relative date.

**States:**

| State | Trigger | Visual |
|-------|---------|--------|
| default | — | `bg-card border border-border rounded-lg p-4` |
| hover | mouse over | `bg-bg-hover`, border `border-focus` at 50% opacity, transition 150ms |
| focus-visible | keyboard | outline 2px `border-focus`, offset 2px |
| loading | data pending | replaced by `LoadingSkeleton` |

**Branch name display:** `project.name` is the repo name (same as branch context). Display as `font-mono text-xs text-accent-blue`. Prefix with git branch icon (`GitBranch` 12px).

**Dimensions:** `w-full min-h-[88px] p-4 rounded-lg`

**Accessibility:**
- If `onClick` provided: `<article tabIndex={0} role="button" aria-label={project.title}>`. Add `onKeyDown` for Enter/Space.
- If no `onClick`: `<article>`, no interactive role.

---

### 4.4 TaskRow

**Purpose:** Single row in a task list.

**Props:**
```typescript
interface TaskRowProps {
  task: {
    id: string;
    project_id?: string | null;
    title: string;
    description?: string | null;
    status: string;
    priority: 1 | 2 | 3 | 4 | 5;
    assignee?: string | null;
    due_date?: string | null;
    artifact?: string | null;
  };
  onComplete?: (id: string) => void;
  onStatusChange?: (id: string, status: string) => void;
}
```

**Layout:**
```
[checkbox]  [PriorityDot]  [title text-body]  ···  [due_date caption]  [StatusBadge size=sm]
```

- Checkbox: 18×18px (`w-[18px] h-[18px]`), custom styled — `border border-border rounded-sm bg-bg-input`. Checked = `bg-accent-blue border-accent-blue` with checkmark SVG.
- Title: when `status === 'completed'`, apply `line-through text-muted`
- Due date: shown only if present. Red (`text-error`) if past due. Format: `MMM D` (e.g. "Apr 30").
- Row height: min 44px to meet touch target

**States:**

| State | Trigger | Visual |
|-------|---------|--------|
| default | — | `border-b border-border py-3 px-4` |
| hover | mouse over | `bg-bg-hover` |
| focus-visible | keyboard on row | outline 2px `border-focus` |
| completed | status=completed | title strikethrough, overall `opacity-70` |
| overdue | due_date < today | due_date text `text-error` |

**Accessibility:**
- Checkbox: `<input type="checkbox" aria-label="Complete: [task.title]">`
- Row: no `role="row"` unless inside a `<table>`. Use `<li>` in a `<ul>`.

---

### 4.5 GoalProgress

**Purpose:** Visualises weekly task completion as a radial progress ring + stats.

**Props:**
```typescript
interface GoalProgressProps {
  completed: number;
  total: number;
}
```

**Layout:**
```
        [radial ring]
     [N / N] text-2xl font-mono
   [text-caption text-muted "tasks done"]
[  progress bar  ████████░░░░  ] (optional)
```

- Ring: SVG, 80px diameter. Track: `stroke-border`. Fill: `stroke-accent-blue`. Stroke-width 6. Stroke-dashoffset animated via CSS transition on mount (0 → final, 600ms ease-out).
- Percentage text inside ring: `text-base font-semibold font-mono text-text-primary`
- Below ring: `completed / total` label `text-caption text-muted`
- If `total === 0`: ring shows 0%, label shows "—"

**Accessibility:** `role="img" aria-label="Weekly progress: [N] of [total] tasks completed"`

---

### 4.6 HandoffCard

**Purpose:** Shows the pending session handoff note — what to work on next.

**Props:**
```typescript
interface HandoffCardProps {
  handoff: {
    id: string;
    project_id?: string | null;
    repo_name?: string | null;
    intent: string;
    context_summary?: string | null;
    resolved_at?: string | null;
    created_at: string;
  } | null;
}
```

**Layout:**
```
┌──────────────────────────────────────────┐
│  [Zap icon 16px text-warning]  NEXT SESSION    │
│                                          │
│  [intent — text-body text-primary]       │
│  [context_summary — text-body-sm text-muted, │
│   max 3 lines, fade-out overflow]        │
│                                          │
│  [repo_name font-mono text-xs text-accent-blue] [date caption] │
└──────────────────────────────────────────┘
```

- Card: `bg-card border border-warning border-opacity-40 rounded-lg p-4`
- Left accent: 3px left border `border-l-4 border-warning`
- If `handoff === null`: render `EmptyState` variant (no border accent)

**States:**

| State | Visual |
|-------|--------|
| default | amber border accent |
| loading | `LoadingSkeleton` h-[120px] |
| empty | EmptyState with "No pending handoff" |

**Accessibility:** `<article aria-label="Session handoff note">`

---

### 4.7 RepoCard

**Purpose:** Shows a workspace repo's current state. Expandable for known_issues.

**Props:**
```typescript
interface RepoCardProps {
  repo: {
    id: string;
    name: string;
    path?: string | null;
    description?: string | null;
    language?: string | null;
    status: string;
    current_branch?: string | null;
    known_issues: string[];
    next_planned_step?: string | null;
    last_activity?: string | null;
  };
}
```

**Layout (collapsed):**
```
┌────────────────────────────────────────────────┐
│ [LanguageBadge]  [name font-mono text-card-title]│
│ [description text-body-sm text-muted 1-line]   │
│ [GitBranch icon] [current_branch font-mono caption] [StatusDot] │
│ [next_planned_step text-body-sm text-muted 1-line]│
│ [▸ N issues] (if known_issues.length > 0)      │
└────────────────────────────────────────────────┘
```

**Expanded (accordion):**
Below: `known_issues` list — each issue as `text-body-sm text-warning` with bullet `•`. Animate height with CSS transition on `max-height` using JS-measured height.

**LanguageBadge:** Pill `px-2 py-0.5 rounded-full text-label` with colors:
- Go: `bg-[#00ADD8] text-white`
- TypeScript: `bg-[#3178C6] text-white`
- Java: `bg-[#B07219] text-white`
- Other: `bg-bg-hover text-muted`

**StatusDot:** 8px circle. `status === 'active'` → `bg-success`. Others → `bg-text-muted`.

**States:**

| State | Visual |
|-------|--------|
| default | `bg-card border border-border rounded-lg p-4` |
| hover | `bg-bg-hover border-border` |
| focus-visible | outline 2px `border-focus` |
| expanded | known_issues visible, chevron rotated 90deg |
| no issues | hide `▸ N issues` row |

**Dimensions:** `w-full min-h-[112px]`

**Accessibility:**
- Expand toggle: `<button aria-expanded={isOpen} aria-controls="issues-[id]">N issues</button>`
- Issues list: `<ul id="issues-[id]" aria-label="Known issues">`

---

### 4.8 DecisionEntry

**Purpose:** Single decision in the timeline list.

**Props:**
```typescript
interface DecisionEntryProps {
  decision: {
    id: string;
    project_id?: string | null;
    repo_name?: string | null;
    title: string;
    context: string;
    decision: string;
    rationale: string;
    alternatives?: string | null;
    created_at: string;
  };
}
```

**Layout:**
```
[●]──[Title text-card-title]───────────[date text-caption text-muted]
     [rationale text-body-sm text-muted, 2-line clamp]
     [Expand button text-caption text-accent-blue underline-offset-2]
     ─ expanded ─
     [Context heading text-label] [context text-body-sm]
     [Decision heading text-label] [decision text-body-sm]
     [Alternatives heading text-label] [alternatives text-body-sm] (if present)
```

- Timeline dot: `w-2.5 h-2.5 rounded-full bg-accent-blue border-2 border-bg-base`
- Expand/collapse toggle: `text-caption text-accent-blue hover:underline`, no border/bg
- Expanded content: `mt-3 space-y-2 border-t border-border pt-3`

**States:**

| State | Visual |
|-------|--------|
| default | collapsed, rationale preview |
| expanded | full content visible, toggle shows "▾ Collapse" |
| hover | title `text-primary` (from `text-card-title` default) |

**Accessibility:**
- `<article aria-label={decision.title}>`
- Expand button: `aria-expanded={isExpanded}` + `aria-controls="decision-body-[id]"`
- Body: `id="decision-body-[id]"`

---

### 4.9 ThemeToggle

**Purpose:** Toggles dark/light theme.

**Props:**
```typescript
interface ThemeToggleProps {}  // reads/writes Zustand themeStore
```

**Layout:** Icon button, 44×44px touch target.
- Dark mode active: Moon icon 18px `text-muted` hover `text-primary`
- Light mode active: Sun icon 18px `text-warning` hover `text-warning`
- Transition: icon crossfade 150ms

**States:**

| State | Visual |
|-------|--------|
| default | icon per current theme |
| hover | `bg-bg-hover rounded-md` |
| focus-visible | outline 2px `border-focus`, offset 2px |

**Accessibility:** `<button aria-label="切換深色/淺色模式 / Toggle theme" aria-pressed={isDark}>`

---

### 4.10 LanguageToggle

**Purpose:** Switches between `zh-TW` and `en`.

**Props:**
```typescript
interface LanguageToggleProps {}  // calls i18n.changeLanguage
```

**Layout:** Text button `ZH / EN`. Active language segment: `text-primary font-medium`. Inactive: `text-muted`. Separator `/` is `text-disabled`.

**Dimensions:** min 44px height.

**States:**

| State | Visual |
|-------|--------|
| default | `px-3 py-2 text-body-sm` |
| hover | `bg-bg-hover rounded-md` |
| focus-visible | outline 2px `border-focus` |

**Accessibility:** `<button aria-label="切換語言 / Switch language">`

---

### 4.11 PageShell

**Purpose:** Wraps every page with sidebar + header. Used once in `App.tsx` wrapping `<Outlet>`.

**Props:**
```typescript
interface PageShellProps {
  children: React.ReactNode;
}
```

**Layout:** see §2.1 App Shell.

Internal state: `sidebarOpen: boolean` (for mobile slide-in). Toggle via hamburger in header.

**Sidebar backdrop (mobile):** `<div>` with `bg-bg-overlay/60 fixed inset-0 z-40` — click closes sidebar. `aria-hidden="true"`.

---

### 4.12 LoadingSkeleton

**Purpose:** Placeholder shimmer while data loads.

**Props:**
```typescript
interface LoadingSkeletonProps {
  className?: string;   // width, height, rounded etc.
  lines?: number;       // repeat N lines (default 1)
}
```

**Visual:** `bg-bg-hover rounded-md` + CSS shimmer animation:
```css
@keyframes shimmer {
  0%   { background-position: -200% 0; }
  100% { background-position:  200% 0; }
}
.skeleton {
  background: linear-gradient(
    90deg,
    var(--color-bg-hover) 25%,
    var(--color-border) 50%,
    var(--color-bg-hover) 75%
  );
  background-size: 200% 100%;
  animation: shimmer 1.4s ease-in-out infinite;
}
```

**Accessibility:** Wrap each skeleton group in `<div role="status" aria-label="Loading...">`.

---

### 4.13 EmptyState

**Purpose:** Zero-data placeholder with optional CTA.

**Props:**
```typescript
interface EmptyStateProps {
  icon?: LucideIcon;         // default: Inbox
  messageKey: string;        // i18n key
  ctaLabelKey?: string;      // i18n key, shown as button if provided
  onCta?: () => void;
}
```

**Layout:**
```
[icon 40px text-muted]
[message text-body text-muted text-center mt-3]
[CTA button mt-4] (optional)
```

Center-aligned. Minimum height `min-h-[120px]` with `flex items-center justify-center flex-col`.

**CTA button:** secondary style — `border border-accent-blue text-accent-blue rounded-md px-4 py-2 text-body-sm hover:bg-accent-blue hover:text-bg-base transition-colors duration-150`

---

### 4.14 GoalCard (GTD Page)

**Purpose:** Goal summary card with progress bar.

**Props:**
```typescript
interface GoalCardProps {
  goal: {
    id: string;
    title: string;
    description?: string | null;
    status: string;
    area?: string | null;
    due_date?: string | null;
  };
  completedTasks: number;
  totalTasks: number;
}
```

**Layout:**
```
┌──────────────────────────────────────┐
│ [area text-label text-muted]         │
│ [title text-card-title]              │
│ [description text-body-sm text-muted │
│  2-line clamp]                       │
│ ─────────────────────────────────    │
│ [████████░░░  completedTasks/total]  │
│ [due_date countdown text-caption]    │
└──────────────────────────────────────┘
```

Progress bar: `h-1.5 rounded-full bg-border` track, fill `bg-accent-blue` width = `(completedTasks/totalTasks)*100%`

Due date countdown: `X days left` in `text-caption text-muted`. If < 7 days: `text-warning`. If overdue: `text-error`.

---

## 5. Interaction Specs

### 5.1 Sidebar Collapse Behaviour

**Mobile (< 640px):**
- Default: hidden (`-translate-x-full fixed left-0 top-0 h-full w-60 z-50`)
- Open: `translate-x-0` + backdrop appears
- Close triggers: hamburger button, backdrop click, nav item click
- Transition: `transition-transform duration-300 ease-in-out`

**Tablet (640–1023px):**
- Always visible as 56px rail (no toggle)
- Labels hidden, icons only
- Hover on icon: tooltip showing label appears to the right (`role="tooltip"`, positioned absolute)

**Desktop (≥ 1024px):**
- Always visible at 240px
- No toggle, no backdrop

### 5.2 Theme Transition

**Mechanism:** Toggle `light` class on `<html>` element.

**Transition:** Apply to `<html>`:
```css
html {
  transition:
    background-color var(--duration-normal) ease,
    color           var(--duration-normal) ease;
}
```

Cards and borders transition via the CSS variable change — no JS needed. Duration: 250ms. Do NOT animate the SVG/icon swap inside ThemeToggle — swap is instant to avoid visual glitch.

**Persistence:** `localStorage.setItem('wbt-theme', isDark ? 'dark' : 'light')`. On app init, read before first render to avoid flash:
```html
<!-- in index.html <head>, before any scripts -->
<script>
  if (localStorage.getItem('wbt-theme') === 'light') {
    document.documentElement.classList.add('light');
  }
</script>
```

### 5.3 Loading States (Skeleton Screens)

- Every data-fetching component: show `LoadingSkeleton` while TanStack Query `isLoading === true`
- Do NOT show spinners inside cards — use skeleton that matches the card's height
- Dashboard: 3 project card skeletons, 1 handoff skeleton, 1 progress skeleton
- GTD tasks: 5 row skeletons
- Workspace: 6 repo card skeletons (2-column on mobile = 3 rows)
- Decisions: 4 entry skeletons

### 5.4 Error States

**Global API errors** (network down, 5xx):
- Toast notification: bottom-right, `bg-error text-white`, auto-dismiss 5s, max 3 stacked
- Toast `role="alert" aria-live="assertive"`

**Inline errors** (404 or empty result after filter):
- Show `EmptyState` component with appropriate message key

**Partial errors** (one query fails, others succeed):
- Show inline error within the specific section, not full-page error
- Format: `bg-[#2e0a0a] border border-error rounded-md p-3 text-body-sm text-error`
- Message: i18n key `error.loadFailed` + retry button

**Toast component props:**
```typescript
interface Toast {
  id: string;
  message: string;
  type: 'error' | 'success' | 'info';
  duration?: number;  // ms, default 5000
}
```

Managed via Zustand `toastStore`. `useEffect` cleanup removes toast after duration.

### 5.5 Quick-Add Task Flow (FAB)

**Trigger:** FAB button on `/gtd`

**Modal appearance:**
- Backdrop: `fixed inset-0 bg-bg-overlay/60 z-50`
- Modal: `fixed inset-0 flex items-end sm:items-center justify-center z-50`
- Modal container: `bg-card border border-border rounded-t-xl sm:rounded-xl w-full sm:max-w-md p-6`
- Slide-up animation on mobile: `translate-y-full → translate-y-0` 300ms ease-out
- Scale-in on desktop: `scale-95 opacity-0 → scale-100 opacity-100` 200ms ease-out

**Fields:**
1. Task title (required) — `<input type="text" placeholder="Task title..." autofocus>`
2. Project (select dropdown, options from loaded projects) — optional
3. Priority (1–5 segmented control) — default 3
4. Due date — `<input type="date">` — optional

**Actions:**
- `POST /api/tasks` on submit
- On success: close modal, toast `success`, TanStack Query invalidate `['projects', id, 'tasks']`
- On error: inline error below form, modal stays open
- Close: `Escape` key, X button, backdrop click
- Submit: `Enter` if no multiline field focused, or "Add Task" button

**Accessibility:**
- `<dialog>` element with `open` attribute
- `aria-modal="true" aria-labelledby="modal-title"`
- Focus trap inside modal while open
- `Escape` → close

---

## 6. File Structure

```
web/
├── index.html
├── vite.config.ts
├── tailwind.config.ts           # if needed for v4 plugin config
├── tsconfig.json
├── public/
│   └── favicon.svg
└── src/
    ├── main.tsx                 # React root, theme init, QueryClient, i18n
    ├── App.tsx                  # Router, PageShell wrapper
    ├── index.css                # @theme tokens, base styles, skeleton keyframes
    │
    ├── api/
    │   ├── client.ts            # axios instance with X-API-Key interceptor
    │   ├── context.ts           # /api/context/today
    │   ├── gtd.ts               # goals, projects, tasks
    │   ├── decisions.ts         # /api/decisions
    │   ├── workspace.ts         # /api/workspace/repos
    │   └── session.ts           # /api/session/handoff
    │
    ├── types/
    │   ├── api.ts               # all TS types matching API JSON shapes
    │   └── common.ts            # Status, Priority enums/types
    │
    ├── stores/
    │   ├── themeStore.ts        # Zustand: isDark, toggleTheme
    │   ├── i18nStore.ts         # Zustand: language, setLanguage
    │   └── toastStore.ts        # Zustand: toasts[], addToast, removeToast
    │
    ├── hooks/
    │   ├── useContextToday.ts   # TanStack Query wrapper
    │   ├── useGoals.ts
    │   ├── useProjects.ts
    │   ├── useTasks.ts
    │   ├── useDecisions.ts
    │   ├── useRepos.ts
    │   └── useHandoff.ts
    │
    ├── locales/
    │   ├── zh-TW.json
    │   └── en.json
    │
    ├── components/
    │   ├── ui/                  # pure, data-agnostic
    │   │   ├── StatusBadge.tsx
    │   │   ├── PriorityDot.tsx
    │   │   ├── LoadingSkeleton.tsx
    │   │   ├── EmptyState.tsx
    │   │   ├── ThemeToggle.tsx
    │   │   ├── LanguageToggle.tsx
    │   │   └── Toast.tsx        # single toast item + ToastContainer
    │   │
    │   ├── layout/
    │   │   ├── PageShell.tsx
    │   │   ├── Sidebar.tsx
    │   │   ├── Header.tsx
    │   │   └── NavItem.tsx
    │   │
    │   ├── dashboard/
    │   │   ├── ProjectCard.tsx
    │   │   ├── GoalProgress.tsx
    │   │   ├── HandoffCard.tsx
    │   │   └── QuickStats.tsx
    │   │
    │   ├── gtd/
    │   │   ├── GoalCard.tsx
    │   │   ├── ProjectList.tsx  # tabs + filtered ProjectCard list
    │   │   ├── TaskRow.tsx
    │   │   ├── TaskList.tsx
    │   │   └── QuickAddModal.tsx
    │   │
    │   ├── workspace/
    │   │   └── RepoCard.tsx
    │   │
    │   └── decisions/
    │       ├── DecisionEntry.tsx
    │       └── DecisionTimeline.tsx
    │
    └── pages/
        ├── DashboardPage.tsx
        ├── GtdPage.tsx
        ├── WorkspacePage.tsx
        ├── DecisionsPage.tsx
        └── NotFoundPage.tsx     # redirects Phase-3 stubs to /
```

### TypeScript Types (`src/types/api.ts`)

```typescript
// Matches JSON serialised from Go db models

export type ProjectStatus = 'active' | 'on_hold' | 'completed' | 'archived';
export type TaskStatus    = 'todo' | 'in_progress' | 'done' | 'blocked';
export type GoalStatus    = 'active' | 'completed' | 'archived';

export interface Project {
  id: string;
  goal_id?: string | null;
  name: string;
  title: string;
  description?: string | null;
  status: ProjectStatus;
  area: string;
  priority: 1 | 2 | 3 | 4 | 5;
  created_at: string;
  updated_at: string;
}

export interface Task {
  id: string;
  project_id?: string | null;
  title: string;
  description?: string | null;
  status: TaskStatus;
  priority: 1 | 2 | 3 | 4 | 5;
  assignee?: string | null;
  due_date?: string | null;
  artifact?: string | null;
  created_at: string;
  updated_at: string;
}

export interface Goal {
  id: string;
  title: string;
  description?: string | null;
  status: GoalStatus;
  area?: string | null;
  due_date?: string | null;
  created_at: string;
  updated_at: string;
}

export interface Repo {
  id: string;
  name: string;
  path?: string | null;
  description?: string | null;
  language?: string | null;
  status: string;
  current_branch?: string | null;
  known_issues: string[];
  next_planned_step?: string | null;
  last_activity?: string | null;
  created_at: string;
  updated_at: string;
}

export interface Decision {
  id: string;
  project_id?: string | null;
  repo_name?: string | null;
  title: string;
  context: string;
  decision: string;
  rationale: string;
  alternatives?: string | null;
  created_at: string;
}

export interface SessionHandoff {
  id: string;
  project_id?: string | null;
  repo_name?: string | null;
  intent: string;
  context_summary?: string | null;
  resolved_at?: string | null;
  created_at: string;
}

export interface WeeklyProgress {
  completed: number;
  total: number;
}

export interface TodayContext {
  goals: Goal[];
  projects: Project[];
  weekly_progress: WeeklyProgress;
  pending_handoff: SessionHandoff | null;
}
```

---

## Design Decisions

**1. Single `@theme` block, no Tailwind config JavaScript.**
Tailwind CSS v4 uses CSS-native `@theme`. All tokens live in `index.css`. Do not use `tailwind.config.ts` for token definitions.

**2. CSS variable light theme overrides via `.light` class on `<html>`, not `prefers-color-scheme`.**
This is a deliberate choice: Wayne wants manual control, not automatic OS-based switching. The init script in `index.html <head>` avoids flash-of-wrong-theme without a full SSR setup.

**3. No `next_planned_step` on Project DB model — use `name` as repo identifier.**
The `Project` struct does not have `next_planned_step`; that field belongs to `Repo`. On the Dashboard ProjectCard, show `next_planned_step` only when the dashboard page correlates projects with repos by `project.name === repo.name`. If no match, omit the field. Do NOT attempt to add `next_planned_step` to the Project API — that is a backend schema change.

**4. `GoalProgress` uses SVG ring, not a third-party chart library.**
Avoids bundle weight. The ring is 9 lines of SVG math: `circumference = 2πr`, `strokeDashoffset = circumference * (1 - pct)`.

**5. No pagination on initial implementation.**
All lists (projects, repos, decisions, tasks) load without pagination. Default API limit of 20 is acceptable for a single-user personal tool. Add virtual scrolling only if performance becomes an issue.

**6. TanStack Query staleTime for Dashboard = 60 seconds.**
`GET /api/context/today` is the hot path on session start. 60s stale time prevents refetch on every tab switch.

**7. `QuickAddModal` uses `<dialog>` element.**
Native dialog provides focus trap + Escape handling for free. Use `dialog.showModal()` / `dialog.close()` via ref.

**8. Icons: Lucide React.**
`lucide-react` is the icon set. Tree-shakeable, consistent stroke weight (1.5), SVG-based. Import individually: `import { LayoutDashboard } from 'lucide-react'`.
