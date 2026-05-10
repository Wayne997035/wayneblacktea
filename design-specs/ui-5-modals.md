# UI-5 — Goal / Project / Decision CRUD Modal Blueprints

Status: design draft (Spec Mode), pending Lead review then frontend-engineer dispatch.
Audience: frontend-engineer implementing the modal set + supporting hooks/i18n.
Stack assumed: React 19 + TS 5.9 + Vite 7 + TanStack Query v5 + Tailwind v4 (CSS-vars only) + Zustand 5 + react-i18next + lucide-react. **No new top-level dependencies.** No `react-hook-form` (not in `web/package.json`); use native `<form>` + `useState` per the existing `CreateGoalModal` / `CreateProjectModal` / `QuickAddModal` precedent.

---

## 0. Scope summary

Five modals (3 Create + 2 Edit). The two existing `Create*Modal`s are **kept** but normalized; the one missing Create modal is `DecisionCreateModal`; the two Edit modals are new.

| Modal                  | Action | Status today                                                                  | Trigger                                          |
| ---------------------- | ------ | ----------------------------------------------------------------------------- | ------------------------------------------------ |
| `GoalCreateModal`      | create | exists as `CreateGoalModal.tsx` — normalize file/identifier name + status field added | GTD page FAB → "Add Goal"                        |
| `GoalEditModal`        | edit   | NEW — backend lacks PATCH; gated (see §6.7)                                   | `GoalCard` edit affordance (NEW)                 |
| `ProjectCreateModal`   | create | exists as `CreateProjectModal.tsx` — normalize + add `status` field           | GTD page FAB → "Add Project"                     |
| `ProjectEditModal`     | edit   | NEW — backend has only PATCH /status, full edit gated (see §6.7)              | `ProjectCard` edit affordance + ProjectDetailPage|
| `DecisionCreateModal`  | create | NEW — `useLogDecision` mutation already exists                                | DecisionsPage header button (NEW) + ProjectDetailPage |

> **DecisionEditModal is explicitly out of scope** (decisions are append-only by domain semantics; reviser only via `log_decision` for a NEW decision that supersedes the previous one). Document in §10 open question if reviewer wants it.

---

## 1. Modal pattern decision

### 1.1 What exists today

Three modals already follow a uniform pattern in `web/src/components/gtd/`:

- `CreateGoalModal.tsx`
- `CreateProjectModal.tsx`
- `QuickAddModal.tsx`

All three share the same skeleton (verified in source):

- Native `<dialog>` element with `dialogRef.useRef<HTMLDialogElement>`
- `dialog.showModal()` on mount (browser provides backdrop, ESC handling, focus trap automatically)
- `dialog.addEventListener('close', onClose)` — single source of truth for "modal closed"
- Backdrop click handled via `onClick` on the `<dialog>` itself: `if (e.target === dialogRef.current) dialogRef.current?.close()`
- Inner `<div>` is the visual card (rounded `var(--radius-xl)`, bg `--color-bg-card`, 1px border `--color-border`, padding 24 (`p-6`), `min-width: min(400px, 90vw)`, `max-width: 448px`)
- Header row: `<h2 id="…-modal-title" class="text-section">` + close button (`X` icon from lucide, 32px hit, `aria-label="Close"`)
- `aria-modal="true"`, `aria-labelledby="…-modal-title"` on the `<dialog>`
- Submit button uses `var(--color-accent-blue)` background, full-width 50/50 split with Cancel
- Errors render as a single `<p class="text-body-sm" style={color: var(--color-error)}>` inside the form (NOT per-field today)

### 1.2 Pattern that the new modals MUST follow

**Reuse the existing pattern verbatim.** Specifically:

1. Continue using native `<dialog>` + `showModal()`. `<dialog>` gives:
   - Built-in backdrop (`dialog::backdrop` already styled in `web/src/index.css:145-148`)
   - ESC closes (browser default — fires the `close` event)
   - Focus trap (browser-native; do not implement manually)
   - Top-layer rendering (escapes stacking-context bugs)
2. **Do NOT** copy the `aside`-based focus-trap pattern from `DayDrawer.tsx` (it's correct for slide-in drawers but unnecessary for centered modals; would duplicate browser behaviour and risk double-handling ESC).
3. Backdrop click → close (`if (e.target === dialogRef.current) dialogRef.current?.close()`).
4. Focus on first input via `autoFocus` on the primary text input (matches existing pattern). Do NOT manually `.focus()` in a `useEffect` — the browser handles initial focus when `showModal()` runs.
5. Focus return on close: `<dialog>` automatically returns focus to the element that triggered `showModal()`. **Verify this with a Playwright/Vitest test** (see §8).
6. The opening page component owns the `activeModal` state and renders `{activeModal === 'goal' && <GoalCreateModal …/>}` (see `GtdPage.tsx:173-187` for precedent). Modal calls `dialogRef.current?.close()` and the `close` listener fires `onClose` → parent unmounts the modal.

### 1.3 Edit-vs-Create variant strategy

Two acceptable approaches; choose **A**:

- **(A) Single component per entity, optional `entity` prop** — `GoalModal({ entity?: Goal, onClose })`. When `entity` is given → Edit mode (pre-fill, title = "Edit Goal", submit calls `useUpdateGoal`); else Create mode. Internal branching is cheap and avoids 200-line duplication.
- (B) Separate `GoalCreateModal` / `GoalEditModal` files — DRY-violating for ~10 lines of branching. Only justified if the field set diverges substantially. **Not chosen** because Create and Edit share identical fields for all three entities.

Naming: rename existing `CreateGoalModal.tsx` → `GoalModal.tsx`, `CreateProjectModal.tsx` → `ProjectModal.tsx`, and add `DecisionModal.tsx`. Leave the file old default-export shims if the build flags any internal imports (none expected — only `GtdPage.tsx` imports them).

---

## 2. Form field components (proposal)

### 2.1 What exists

`web/src/components/ui/` contains: `EmptyState`, `LanguageToggle`, `LoadingSkeleton`, `PriorityDot`, `StatusBadge`, `ThemeToggle`, `Toast`. **No `<FormField>` / `<FormError>` / `<TextInput>` exist.** Each modal currently inlines the same `<label>` + `<input>` + focus-border-toggle handlers — ~25 lines per field.

### 2.2 Proposal: extract a `<FormField>` wrapper

**Scope of extraction**: ONLY label + the focus-border style boilerplate. Do NOT abstract over input vs select vs textarea (keeps API minimal — `children` prop receives the raw input).

```tsx
// web/src/components/ui/FormField.tsx
import { ReactNode } from 'react'

export interface FormFieldProps {
  id: string;
  label: ReactNode;
  /** Optional caption shown beneath the input, e.g. "kebab-case identifier" */
  hint?: ReactNode;
  /** Per-field error message (overrides hint when present) */
  error?: string;
  /** Renders a small "*" after the label. Pure visual; required validation is owned by the form. */
  required?: boolean;
  children: ReactNode;
}

export function FormField({ id, label, hint, error, required, children }: FormFieldProps) {
  const describedBy = error ? `${id}-err` : hint ? `${id}-hint` : undefined
  return (
    <div>
      <label
        htmlFor={id}
        className="text-label block mb-1"
        style={{ color: 'var(--color-text-muted)' }}
      >
        {label}
        {required && (
          <span aria-hidden="true" style={{ color: 'var(--color-error)' }}> *</span>
        )}
      </label>
      {/* children MUST be a single input/select/textarea with id={id} and aria-describedby={describedBy} */}
      {children}
      {error ? (
        <p id={`${id}-err`} role="alert" className="text-body-sm mt-1" style={{ color: 'var(--color-error)' }}>
          {error}
        </p>
      ) : hint ? (
        <p id={`${id}-hint`} className="text-caption mt-1" style={{ color: 'var(--color-text-muted)' }}>
          {hint}
        </p>
      ) : null}
    </div>
  )
}
```

### 2.3 Proposal: shared input style helpers

To avoid copy-paste of the focus-border toggle:

```tsx
// web/src/components/ui/formStyles.ts
import type { CSSProperties, FocusEvent } from 'react'

export const inputBaseStyle: CSSProperties = {
  background: 'var(--color-bg-input)',
  border: '1px solid var(--color-border)',
  color: 'var(--color-text-primary)',
  outline: 'none',
}

export function onInputFocus(e: FocusEvent<HTMLElement>): void {
  ;(e.currentTarget as HTMLElement).style.borderColor = 'var(--color-border-focus)'
}
export function onInputBlur(e: FocusEvent<HTMLElement>): void {
  ;(e.currentTarget as HTMLElement).style.borderColor = 'var(--color-border)'
}
```

Why no Tailwind class? The existing modals already commit to inline `style={…}` against CSS variables (see `CreateGoalModal.tsx:118-127`); switching to Tailwind classes mid-refactor is out of scope for UI-5.

> Note: `:focus-visible` is already globally styled in `index.css:151-155` (2px outline `var(--color-border-focus)`). The custom `onFocus`/`onBlur` border swap is **decorative** and safe to keep — global outline still applies.

### 2.4 Per-modal usage pattern

```tsx
<FormField id="goal-title" label={t('goals.modal.titleLabel')} required error={errors.title}>
  <input
    id="goal-title"
    type="text"
    autoFocus
    value={title}
    onChange={(e) => setTitle(e.target.value)}
    placeholder={t('goals.modal.titlePlaceholder')}
    maxLength={200}
    className="w-full rounded-md px-3 py-2 text-body"
    style={inputBaseStyle}
    onFocus={onInputFocus}
    onBlur={onInputBlur}
    aria-describedby={errors.title ? 'goal-title-err' : undefined}
  />
</FormField>
```

---

## 3. Per-modal layout sketches

All three modals follow the same shell (header, `<form>`, footer with Cancel + Submit), identical to `CreateGoalModal.tsx`. Only the field block differs.

### 3.1 GoalModal (Create + Edit)

```
┌─────────────────────────────────────────────────────┐
│  Add Goal / Edit Goal                            X  │   ← h2 id="goal-modal-title"
├─────────────────────────────────────────────────────┤
│  TITLE *                                            │
│  [Goal title…                                    ]  │   ← input, autoFocus, maxLength=200
│                                                     │
│  AREA (OPTIONAL)                                    │
│  [e.g. Career, Health…                           ]  │   ← input, maxLength=80
│                                                     │
│  STATUS                                             │   ← (NEW field; Create defaults "active")
│  [active ▼]                                         │   ← select: active | completed | archived
│                                                     │
│  DESCRIPTION (OPTIONAL)                             │
│  ┌─────────────────────────────────────────────┐   │
│  │ What does achieving this goal look like?     │   │   ← textarea rows=3, maxLength=2000
│  │                                              │   │
│  └─────────────────────────────────────────────┘   │
│  0 / 2000                                           │   ← live char count (caption, muted)
│                                                     │
│  TARGET DATE (OPTIONAL)                             │
│  [📅 yyyy-mm-dd]                                   │   ← input type="date", colorScheme: dark
│                                                     │
│  [error message if any]                             │   ← global form error (red)
│                                                     │
│  [   Cancel    ]  [   Save / Add Goal    ]          │   ← 50/50 split, Submit = accent-blue
└─────────────────────────────────────────────────────┘
```

**Field list (in tab order):**

| # | Field        | Type             | Required | i18n key                                | Width | Notes                                         |
| - | ------------ | ---------------- | -------- | --------------------------------------- | ----- | --------------------------------------------- |
| 1 | title        | text             | yes      | `goals.modal.titleLabel`                | full  | autoFocus on Create; max 200; trim before submit |
| 2 | area         | text             | no       | `goals.modal.areaLabel`                 | full  | max 80; trim                                  |
| 3 | status       | select           | yes      | `goals.modal.statusLabel`               | full  | options: `active` `completed` `archived`; default `active` on Create |
| 4 | description  | textarea (rows=3)| no       | `goals.modal.descriptionLabel`          | full  | max 2000; show "n / 2000" caption when > 0   |
| 5 | target_date  | date             | no       | `goals.modal.targetDateLabel`           | full  | maps to `due_date` in API (already exists in `Goal` type) |

> **Field rename**: spec calls it "target_date" but `Goal.due_date` is the API field — keep label "Target date" in UI, payload key `due_date`. Document in §10 — should the API column be renamed to `target_date` for clarity? (Out of scope for UI-5; flag only.)

### 3.2 ProjectModal (Create + Edit)

```
┌─────────────────────────────────────────────────────┐
│  Add Project / Edit Project                      X  │
├─────────────────────────────────────────────────────┤
│  TITLE *                                            │
│  [Project title…                                 ]  │   ← maxLength=200
│                                                     │
│  REPO / SLUG NAME *                                 │
│  [kebab-case-identifier                          ]  │   ← font-mono; pattern validation; max 64
│  Lowercase letters, digits, hyphens                 │   ← hint when no error
│                                                     │
│  AREA (OPTIONAL)                                    │
│  [e.g. Engineering, Product…                     ]  │   ← max 80
│                                                     │
│  DESCRIPTION (OPTIONAL)                             │
│  ┌─────────────────────────────────────────────┐   │
│  │ What is this project about?                  │   │   ← textarea rows=2, max 2000
│  └─────────────────────────────────────────────┘   │
│                                                     │
│  LINKED GOAL (OPTIONAL)                             │
│  [— ▼]                                              │   ← only rendered if goals.length > 0
│                                                     │
│  STATUS                                             │   ← (NEW field; Create defaults "active")
│  [active ▼]                                         │   ← select: active | on_hold | completed | archived
│                                                     │
│  PRIORITY                                           │
│  [ 1 ][ 2 ][ 3 ][ 4 ][ 5 ]                          │   ← 5 buttons, default 3, accent-blue when selected
│                                                     │
│  [error message]                                    │
│                                                     │
│  [   Cancel    ]  [   Save / Add Project    ]       │
└─────────────────────────────────────────────────────┘
```

| # | Field       | Type            | Required | i18n key                          | Notes                                                   |
| - | ----------- | --------------- | -------- | --------------------------------- | ------------------------------------------------------- |
| 1 | title       | text            | yes      | `projects.modal.titleLabel`       | max 200                                                 |
| 2 | name        | text            | yes      | `projects.modal.nameLabel`        | max 64; pattern `^[a-z0-9]+(-[a-z0-9]+)*$`; **disabled in Edit mode** (slug is identity) |
| 3 | area        | text            | no       | `projects.modal.areaLabel`        | max 80                                                  |
| 4 | description | textarea (rows=2)| no      | `projects.modal.descriptionLabel` | max 2000                                                |
| 5 | goal_id     | select          | no       | `projects.modal.goalLabel`        | only rendered if `goals.length > 0`; sends `null` when "—" |
| 6 | status      | select          | yes      | `projects.modal.statusLabel`      | `active`, `on_hold`, `completed`, `archived`; default `active` on Create |
| 7 | priority    | button group 1–5| yes      | `projects.modal.priorityLabel`    | reuse the existing button-group pattern from `CreateProjectModal.tsx:255-272` |

### 3.3 DecisionCreateModal

This is a denser modal — five textareas. Increase max-width to `560px` (vs `448px` for Goal/Project) so multi-line bodies stay readable. Title "Log Decision" / "新增決策".

```
┌────────────────────────────────────────────────────────────┐
│  Log Decision                                            X │
├────────────────────────────────────────────────────────────┤
│  TITLE *                                                   │
│  [What was decided in one line?                         ]  │   ← max 200
│                                                            │
│  CONTEXT *                                                 │
│  ┌──────────────────────────────────────────────────────┐ │
│  │ What prompted this decision?                          │ │   ← rows=3, max 4000
│  │                                                       │ │
│  └──────────────────────────────────────────────────────┘ │
│  0 / 4000                                                  │   ← live char count
│                                                            │
│  DECISION *                                                │
│  ┌──────────────────────────────────────────────────────┐ │
│  │ What did we decide?                                   │ │   ← rows=3, max 4000
│  └──────────────────────────────────────────────────────┘ │
│                                                            │
│  RATIONALE *                                               │
│  ┌──────────────────────────────────────────────────────┐ │
│  │ Why this and not the alternatives?                    │ │   ← rows=3, max 4000
│  └──────────────────────────────────────────────────────┘ │
│                                                            │
│  ALTERNATIVES (OPTIONAL)                                   │
│  ┌──────────────────────────────────────────────────────┐ │
│  │ Other options considered                              │ │   ← rows=2, max 4000
│  └──────────────────────────────────────────────────────┘ │
│                                                            │
│  REPO (OPTIONAL)              PROJECT (OPTIONAL)           │
│  [free-text repo name      ]  [— ▼                      ]  │   ← side-by-side on ≥640px (sm:); stacked on mobile
│                                                            │
│  [error message]                                           │
│                                                            │
│  [    Cancel    ]  [    Log Decision    ]                  │
└────────────────────────────────────────────────────────────┘
```

| # | Field         | Type             | Required | i18n key                              | Notes                                          |
| - | ------------- | ---------------- | -------- | ------------------------------------- | ---------------------------------------------- |
| 1 | title         | text             | yes      | `decisions.modal.titleLabel`          | max 200; autoFocus                             |
| 2 | context       | textarea (rows=3)| yes      | `decisions.modal.contextLabel`        | max 4000; live counter                         |
| 3 | decision      | textarea (rows=3)| yes      | `decisions.modal.decisionLabel`       | max 4000                                       |
| 4 | rationale     | textarea (rows=3)| yes      | `decisions.modal.rationaleLabel`      | max 4000                                       |
| 5 | alternatives  | textarea (rows=2)| no       | `decisions.modal.alternativesLabel`   | max 4000                                       |
| 6 | repo_name     | text             | no       | `decisions.modal.repoLabel`           | max 80; free-text (mirrors filter usage)       |
| 7 | project_id    | select           | no       | `decisions.modal.projectLabel`        | from `useProjects()`; sends `null` for "—"     |

The "side-by-side on sm and up" rule for the bottom row uses Tailwind: `<div class="flex flex-col sm:flex-row gap-4">…</div>`.

---

## 4. Validation rules

### 4.1 Validation trigger semantics

- **On submit** (`form onSubmit` / button click): full validation; collect ALL errors; abort submit if any.
- **On blur** (per field): validate THAT field; clear its error if it was previously failing.
- **On change** (per field): NEVER show new errors (avoids "yelling while typing"); but DO clear the existing error if the new value passes (matches user expectation of "I fixed it").
- Live char counter for `description` / `context` / `decision` / `rationale` / `alternatives` updates `onChange` but is informational only — the `maxLength` attribute on the input does the hard cap (no error needed).

### 4.2 Per-field rules

| Field                          | Rule                                                  | Error key                                |
| ------------------------------ | ----------------------------------------------------- | ---------------------------------------- |
| `goal.title`                   | `trim().length >= 1`                                  | `goals.modal.errors.titleRequired`       |
| `goal.title`                   | `trim().length <= 200`                                | `goals.modal.errors.titleTooLong`        |
| `goal.area`                    | `trim().length <= 80`                                 | `goals.modal.errors.areaTooLong`         |
| `goal.description`             | `trim().length <= 2000`                               | `goals.modal.errors.descriptionTooLong`  |
| `goal.target_date`             | parseable date OR empty                               | `goals.modal.errors.dateInvalid`         |
| `goal.status`                  | one of `active` `completed` `archived`                | (impossible via `<select>`; no msg)      |
| `project.title`                | `trim().length >= 1` and `<= 200`                     | `projects.modal.errors.titleRequired` / `…titleTooLong` |
| `project.name`                 | `trim().length >= 1`                                  | `projects.modal.errors.nameRequired`     |
| `project.name`                 | `^[a-z0-9]+(-[a-z0-9]+)*$` (no leading/trailing/double hyphens, lowercase only) | `projects.modal.errors.nameSlugInvalid` |
| `project.name`                 | `trim().length <= 64`                                 | `projects.modal.errors.nameTooLong`      |
| `project.area`                 | `trim().length <= 80`                                 | `projects.modal.errors.areaTooLong`      |
| `project.description`          | `trim().length <= 2000`                               | `projects.modal.errors.descriptionTooLong` |
| `project.priority`             | one of 1–5                                            | (impossible via button group; no msg)    |
| `project.status`               | one of `active` `on_hold` `completed` `archived`      | (impossible via `<select>`; no msg)      |
| `decision.title`               | `trim().length >= 1` and `<= 200`                     | `decisions.modal.errors.titleRequired` / `…titleTooLong` |
| `decision.context`             | `trim().length >= 1` and `<= 4000`                    | `decisions.modal.errors.contextRequired` / `…contextTooLong` |
| `decision.decision`            | `trim().length >= 1` and `<= 4000`                    | `decisions.modal.errors.decisionRequired` / `…decisionTooLong` |
| `decision.rationale`           | `trim().length >= 1` and `<= 4000`                    | `decisions.modal.errors.rationaleRequired` / `…rationaleTooLong` |
| `decision.alternatives`        | `trim().length <= 4000`                               | `decisions.modal.errors.alternativesTooLong` |
| `decision.repo_name`           | `trim().length <= 80`                                 | `decisions.modal.errors.repoTooLong`     |

### 4.3 Server-error mapping

| HTTP / body marker          | i18n key shown in form                  |
| --------------------------- | --------------------------------------- |
| `409 Conflict` on `POST /api/projects` | `projects.modal.errors.nameExists` (existing key `gtd.projectNameExists` — keep both as alias OR migrate; chosen: migrate, see §5) |
| `400 Bad Request`           | `common.errors.invalidInput`            |
| `401`                       | global redirect to login (already handled by `apiFetch`) — modal MUST NOT show its own message |
| `429 Too Many Requests`     | `common.errors.rateLimited`             |
| Other / network / 5xx       | `common.errors.saveFailed`              |

The error string lives in the global form error region (`<p style={color: var(--color-error)}>` inside the form, just above the button row), NOT in a toast. Toasts are reserved for **success**.

### 4.4 Slug live-helper for `project.name`

When the title is non-empty AND the user has not typed in the name field yet, suggest a slug derived from the title:

- `slugify(title) = title.toLowerCase().normalize('NFD').replace(/[̀-ͯ]/g, '').replace(/[^a-z0-9]+/g, '-').replace(/^-+|-+$/g, '').slice(0, 64)`
- Render a small clickable hint under the name field: `Suggested: my-cool-project`. Clicking sets the field. Once user has typed anything, hide the suggestion.

---

## 5. i18n keys

New namespaces. Both `en.json` and `zh-TW.json` MUST receive identical keys. **Existing keys retained** (`gtd.addGoal`, `gtd.addProject`, `gtd.goalCreated`, `gtd.projectCreated`, `gtd.projectNameExists`, `error.fieldRequired`, `error.loadFailed`) but the modals MUST migrate to the new namespace below; the old keys stay live for non-modal usages (FAB labels, etc.).

### 5.1 `goals.modal.*`

```json
"goals": {
  "modal": {
    "titleCreate": "Add Goal",
    "titleEdit": "Edit Goal",
    "titleLabel": "Title",
    "titlePlaceholder": "Goal title…",
    "areaLabel": "Area",
    "areaPlaceholder": "e.g. Career, Health…",
    "statusLabel": "Status",
    "descriptionLabel": "Description",
    "descriptionPlaceholder": "What does achieving this goal look like?",
    "targetDateLabel": "Target date",
    "submitCreate": "Add goal",
    "submitEdit": "Save changes",
    "createdToast": "Goal created",
    "updatedToast": "Goal updated",
    "errors": {
      "titleRequired": "Title is required",
      "titleTooLong": "Title must be 200 characters or fewer",
      "areaTooLong": "Area must be 80 characters or fewer",
      "descriptionTooLong": "Description must be 2000 characters or fewer",
      "dateInvalid": "Invalid date"
    }
  },
  "status": {
    "active": "Active",
    "completed": "Completed",
    "archived": "Archived"
  }
}
```

### 5.2 `projects.modal.*`

```json
"projects": {
  "modal": {
    "titleCreate": "Add Project",
    "titleEdit": "Edit Project",
    "titleLabel": "Title",
    "titlePlaceholder": "Project title…",
    "nameLabel": "Repo / slug name",
    "namePlaceholder": "kebab-case identifier…",
    "nameHint": "Lowercase letters, digits, and hyphens",
    "nameSuggestion": "Suggested: {{slug}}",
    "areaLabel": "Area",
    "areaPlaceholder": "e.g. Engineering, Product…",
    "descriptionLabel": "Description",
    "descriptionPlaceholder": "What is this project about?",
    "goalLabel": "Linked goal",
    "goalPlaceholderNone": "—",
    "statusLabel": "Status",
    "priorityLabel": "Priority",
    "submitCreate": "Add project",
    "submitEdit": "Save changes",
    "createdToast": "Project created",
    "updatedToast": "Project updated",
    "errors": {
      "titleRequired": "Title is required",
      "titleTooLong": "Title must be 200 characters or fewer",
      "nameRequired": "Slug is required",
      "nameSlugInvalid": "Use lowercase letters, digits, and single hyphens only",
      "nameTooLong": "Slug must be 64 characters or fewer",
      "nameExists": "A project with this slug already exists",
      "areaTooLong": "Area must be 80 characters or fewer",
      "descriptionTooLong": "Description must be 2000 characters or fewer"
    }
  }
}
```

### 5.3 `decisions.modal.*`

```json
"decisions": {
  "modal": {
    "titleCreate": "Log Decision",
    "titleLabel": "Title",
    "titlePlaceholder": "What was decided in one line?",
    "contextLabel": "Context",
    "contextPlaceholder": "What prompted this decision?",
    "decisionLabel": "Decision",
    "decisionPlaceholder": "What did we decide?",
    "rationaleLabel": "Rationale",
    "rationalePlaceholder": "Why this and not the alternatives?",
    "alternativesLabel": "Alternatives",
    "alternativesPlaceholder": "Other options considered",
    "repoLabel": "Repo",
    "repoPlaceholder": "free-text repo name",
    "projectLabel": "Project",
    "projectPlaceholderNone": "—",
    "submit": "Log decision",
    "createdToast": "Decision logged",
    "errors": {
      "titleRequired": "Title is required",
      "titleTooLong": "Title must be 200 characters or fewer",
      "contextRequired": "Context is required",
      "contextTooLong": "Context must be 4000 characters or fewer",
      "decisionRequired": "Decision is required",
      "decisionTooLong": "Decision must be 4000 characters or fewer",
      "rationaleRequired": "Rationale is required",
      "rationaleTooLong": "Rationale must be 4000 characters or fewer",
      "alternativesTooLong": "Alternatives must be 4000 characters or fewer",
      "repoTooLong": "Repo name must be 80 characters or fewer"
    }
  }
}
```

### 5.4 Shared additions to `common.*`

```json
"common": {
  "save": "Save",
  "cancel": "Cancel",
  "add": "Add",
  "submitting": "Saving…",
  "errors": {
    "saveFailed": "Could not save. Try again.",
    "invalidInput": "Some fields are invalid.",
    "rateLimited": "Too many requests. Try again in a moment.",
    "unsavedChanges": "Discard your changes?"
  },
  "charCount": "{{count}} / {{max}}"
}
```

### 5.5 zh-TW translations (template — engineer fills in)

Engineer translates each leaf string. Reference style from existing `zh-TW.json`:

- "Add Goal" → "新增目標"
- "Edit Goal" → "編輯目標"
- "Title" → "標題"
- "Description" → "描述"
- "Status" → "狀態"
- "Target date" → "目標日期"
- "Add Project" → "新增專案"
- "Edit Project" → "編輯專案"
- "Repo / slug name" → "Slug 識別碼"
- "Lowercase letters, digits, and hyphens" → "小寫字母、數字、連字號"
- "Suggested: {{slug}}" → "建議：{{slug}}"
- "Linked goal" → "關聯目標"
- "Priority" → "優先級"
- "Log Decision" → "新增決策"
- "Context" → "背景"
- "Decision" → "決策"
- "Rationale" → "理由"
- "Alternatives" → "替代方案"
- "Repo" → "Repo"
- "Project" → "專案"
- "Saving…" → "儲存中…"
- "Discard your changes?" → "確定要捨棄修改？"
- "Could not save. Try again." → "儲存失敗，請重試。"
- "Some fields are invalid." → "部分欄位有誤。"
- "Too many requests. Try again in a moment." → "請求過於頻繁，請稍後再試。"
- "{{count}} / {{max}}" → "{{count}} / {{max}}" (numerical, no translation)
- "Goal created" → "目標已建立"
- "Goal updated" → "目標已更新"
- "Project created" → "專案已建立"
- "Project updated" → "專案已更新"
- "Decision logged" → "決策已記錄"

---

## 6. Mutation flow

### 6.1 Hook inventory (current state)

| Hook                     | File                                      | Method | URL                          | Notes                          |
| ------------------------ | ----------------------------------------- | ------ | ---------------------------- | ------------------------------ |
| `useCreateGoal`          | `web/src/hooks/useGoals.ts:19`            | POST   | `/api/goals`                 | exists                         |
| `useUpdateGoal`          | NEW                                       | PATCH  | `/api/goals/:id`             | **backend missing** (see §6.7) |
| `useCreateProject`       | `web/src/hooks/useProjects.ts:29`         | POST   | `/api/projects`              | exists                         |
| `useUpdateProject`       | NEW                                       | PATCH  | `/api/projects/:id`          | **backend missing** (only `/status` endpoint exists per `cmd/server/main.go:208`) |
| `useLogDecision`         | `web/src/hooks/useDecisions.ts:28`        | POST   | `/api/decisions`             | exists                         |

### 6.2 Add `status` to existing create payloads

`CreateGoalRequest` (`web/src/hooks/useGoals.ts:12`) and `CreateProjectRequest` (`web/src/hooks/useProjects.ts:20`) both LACK a `status` field. Backend defaults will apply. **Engineer MUST verify** by reading `internal/gtd/gtd.go` (or wherever `CreateGoal` / `CreateProject` are defined) whether server-side defaults to `active`. If server requires `status` → add to request type; else extending request to include optional `status?: GoalStatus | ProjectStatus` is safe.

### 6.3 New hook: `useUpdateGoal`

```tsx
// web/src/hooks/useGoals.ts (append)
export interface UpdateGoalRequest {
  title?: string;
  area?: string | null;
  description?: string | null;
  status?: GoalStatus;
  due_date?: string | null;
}

export function useUpdateGoal() {
  const queryClient = useQueryClient()
  return useMutation<Goal, Error, { id: string } & UpdateGoalRequest>({
    mutationFn: ({ id, ...body }) =>
      apiFetch<Goal>(`/api/goals/${id}`, {
        method: 'PATCH',
        body: JSON.stringify(body),
      }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['goals'] })
      void queryClient.invalidateQueries({ queryKey: ['context', 'today'] })
    },
  })
}
```

### 6.4 New hook: `useUpdateProject`

```tsx
// web/src/hooks/useProjects.ts (append)
export interface UpdateProjectRequest {
  title?: string;
  area?: string | null;
  description?: string | null;
  status?: ProjectStatus;
  priority?: 1 | 2 | 3 | 4 | 5;
  goal_id?: string | null;
  // name (slug) intentionally omitted — slug is identity, immutable post-create
}

export function useUpdateProject() {
  const queryClient = useQueryClient()
  return useMutation<Project, Error, { id: string } & UpdateProjectRequest>({
    mutationFn: ({ id, ...body }) =>
      apiFetch<Project>(`/api/projects/${id}`, {
        method: 'PATCH',
        body: JSON.stringify(body),
      }),
    onSuccess: (_data, { id }) => {
      void queryClient.invalidateQueries({ queryKey: ['projects'] })
      void queryClient.invalidateQueries({ queryKey: ['projects', id] })
      void queryClient.invalidateQueries({ queryKey: ['context', 'today'] })
    },
  })
}
```

### 6.5 Optimistic vs invalidate

**Decision: invalidate-on-success. NO optimistic updates for create/edit modals in UI-5.** Reasons:

1. The modal closes synchronously on `mutateAsync` success — user sees the result via the next render of the parent list (re-fetched). The latency window is sub-second on local dev and Aiven prod.
2. `useUpdateKnowledge` (`web/src/hooks/useKnowledge.ts:46-66`) is the ONLY existing optimistic hook in the codebase, and only for a single column. Modals touching 5+ fields with backend-side `updated_at`/validation are not a fit for the pattern.
3. The existing `useCreateGoal` / `useCreateProject` / `useLogDecision` already use plain invalidate. Stay consistent.

`onSuccess` MUST invalidate:
- The list query for that entity (`['goals']` / `['projects']` / `['decisions', …]`)
- `['context', 'today']` (Dashboard sidebar consumes this — already done in existing `useCreateGoal` / `useCreateProject`)
- For Project edit: also invalidate `['projects', id]` (single-project query used by `ProjectDetailPage`)
- For Decision: also invalidate `['decisions', 'all']` AND any project-scoped variant (the query key uses `projectId ?? 'all'` per `useDecisions.ts:7`)

### 6.6 Submit flow (pseudocode)

```tsx
async function handleSubmit(e: React.FormEvent) {
  e.preventDefault()
  const errors = validate(form)
  if (errors.size > 0) {
    setFieldErrors(errors)
    setGlobalError(t('common.errors.invalidInput'))
    return
  }
  setFieldErrors(new Map())
  setGlobalError('')
  try {
    if (isEdit) {
      await updateMutation.mutateAsync({ id: entity.id, ...payload })
      addToast({ type: 'success', message: t('…modal.updatedToast') })
    } else {
      await createMutation.mutateAsync(payload)
      addToast({ type: 'success', message: t('…modal.createdToast') })
    }
    dialogRef.current?.close()
  } catch (err) {
    setGlobalError(mapServerError(err)) // see §4.3
  }
}
```

### 6.7 Backend gating for Edit modals (CRITICAL)

`grep` of `cmd/server/main.go` confirms ONLY these write endpoints exist for goals/projects/decisions:

```
POST   /api/goals
POST   /api/projects
PATCH  /api/projects/:id/status
POST   /api/decisions
```

**There is no `PATCH /api/goals/:id` or `PATCH /api/projects/:id`.** Therefore:

- **GoalEditModal MUST NOT ship** in the same PR as backend work for `PATCH /api/goals/:id` (handler + validator + sqlc query + tests).
- **ProjectEditModal MUST NOT ship** in the same PR as backend work for `PATCH /api/projects/:id` (full update beyond `/status`).
- The Lead's choices:
  - **(A)** Split UI-5 into two PRs: (1) Create modals + DecisionCreateModal (no backend change); (2) Edit modals + matching backend handlers.
  - **(B)** Single PR with backend additions in scope. Engineer ships handler + Echo route + sqlc query + integration test alongside the modal.
  - Chosen approach goes in §10 Open Questions for Lead to resolve.

For the spec, both Edit modals are designed as if the endpoints exist; the engineer is told to **stub the mutation** and link a follow-up GTD task if the Lead picks split (A).

---

## 7. Edit vs Create — pre-fill + dirty-state warning

### 7.1 Pre-fill on Edit

```tsx
const initial = useMemo(() => entity ?? defaultGoal(), [entity])
const [title, setTitle] = useState(initial.title)
const [area,  setArea]  = useState(initial.area ?? '')
// …etc
```

For `target_date` / `due_date` (date input expects `YYYY-MM-DD`):

```ts
const initialDate = entity?.due_date
  ? new Date(entity.due_date).toISOString().slice(0, 10)
  : ''
```

### 7.2 Dirty-state confirmation

Track an `isDirty` flag derived from comparing each current state value against `initial`. When the user attempts to close (backdrop click, ESC, close button, Cancel button):

- If `isDirty === false` → close immediately
- If `isDirty === true` → show `window.confirm(t('common.errors.unsavedChanges'))`. If user cancels confirm → keep modal open. If user confirms → close.

Use `window.confirm` rather than building a nested confirm modal (KISS, matches existing one-line confirm patterns; no nested-dialog stacking concerns).

**Implementation notes:**

- ESC handler: register a `keydown` listener on the dialog element that catches Escape, calls `attemptClose()`. The default `<dialog>` ESC behaviour calls `dialog.close()` directly — to intercept, listen for `cancel` event (fired before close on ESC) and `e.preventDefault()` if dirty + user cancels confirm.
- Backdrop click: same — call `attemptClose()` instead of `dialogRef.current?.close()` directly.
- The X button and Cancel button: same — call `attemptClose()`.
- Submit success: bypass `attemptClose()`, call `dialog.close()` directly.

```tsx
function attemptClose() {
  if (!isDirty) {
    dialogRef.current?.close()
    return
  }
  if (window.confirm(t('common.errors.unsavedChanges'))) {
    dialogRef.current?.close()
  }
}

useEffect(() => {
  const dialog = dialogRef.current
  if (!dialog) return
  const onCancel = (e: Event) => {
    if (!isDirty) return // let default close happen
    e.preventDefault()    // suppress default close
    attemptClose()        // run our confirm + maybe-close
  }
  dialog.addEventListener('cancel', onCancel)
  return () => dialog.removeEventListener('cancel', onCancel)
}, [isDirty])
```

> **Do not** use `beforeunload` — that's for tab close, not modal close. Out of scope.

### 7.3 Disable submit while pristine on Edit

Submit button is disabled when `isEdit && !isDirty` to make "no-op save" impossible. On Create, submit is enabled as long as required fields pass; pristine state is irrelevant.

---

## 8. Test plan

Tests live alongside components (`web/src/components/gtd/GoalModal.test.tsx`, etc.) and use `vitest` + `@testing-library/react` + `@testing-library/user-event` (already present in devDependencies). Reference existing `DayDrawer.test.tsx` for patterns.

### 8.1 Per-modal test list

For EACH of `GoalModal`, `ProjectModal`, `DecisionModal`:

1. **Renders dialog with correct title** — Create mode shows `goals.modal.titleCreate`, Edit mode shows `goals.modal.titleEdit`.
2. **First text input has focus on open** — assert `document.activeElement === <title input>`.
3. **ESC closes** when no input is dirty — `fireEvent.keyDown(document, { key: 'Escape' })`; assert `onClose` called.
4. **ESC + dirty → window.confirm called** — mock `window.confirm`, assert it was called once.
5. **Close button (X)** triggers close.
6. **Cancel button** triggers close (and confirms when dirty).
7. **Backdrop click closes** — fire `click` on the `<dialog>` itself with `target === currentTarget`.
8. **Required-field validation** — submit empty form; assert each required field shows its `errors.…Required` message; assert mutation NOT called.
9. **Length validation** — type 201 chars in title; submit; assert `errors.titleTooLong`.
10. **Slug validation (Project only)** — type `Bad Slug` → submit → assert `nameSlugInvalid`. Then `valid-slug` → submit succeeds.
11. **Slug suggestion (Project Create only)** — type title `My Cool Project`; assert hint text `Suggested: my-cool-project` appears; click hint; assert name field is `my-cool-project`.
12. **Slug field is disabled in Edit mode** (Project) — `expect(nameInput).toBeDisabled()`.
13. **Submit success** — fill required fields; mock mutation to resolve; submit; assert mutation called with EXACT payload (trimmed values, `null` for empty optional fields, ISO date for `due_date`); assert toast added (`createdToast` / `updatedToast`); assert dialog closed.
14. **Submit error (5xx)** — mock mutation to reject with `Error('500: …')`; assert global error = `common.errors.saveFailed`; assert toast NOT added; assert dialog stays open.
15. **Submit error 409 (Project create only)** — mock with `Error('409: …')`; assert `projects.modal.errors.nameExists`.
16. **Edit pre-fill** (Goal/Project) — pass an `entity` prop; assert each field input shows the entity's value; assert title shows `…modal.titleEdit`.
17. **Edit "no-op submit" disabled** — open in Edit mode without changing anything; assert submit button is disabled.
18. **Char counter** updates on textarea change — type 5 chars in description; assert "5 / 2000" appears.
19. **i18n** — render with both `en` and `zh-TW`; assert at least the title + submit button render in the expected language. (Use `i18n.changeLanguage` in the test setup.)

### 8.2 Hook tests (new)

- `useUpdateGoal` — given mocked `apiFetch` resolving, mutation invalidates `['goals']` + `['context', 'today']`.
- `useUpdateProject` — same pattern, plus `['projects', id]`.

### 8.3 a11y smoke

Optionally include a `vi.test('passes axe', …)` using `vitest-axe`. **Not required for UI-5** (no axe dep yet, and adding it is a top-level dependency — out of scope per Constraints).

---

## 9. Accessibility checklist

| Requirement                     | How                                                                                    |
| ------------------------------- | -------------------------------------------------------------------------------------- |
| `role="dialog"`                 | Native `<dialog>` element gives this implicitly.                                       |
| `aria-modal="true"`             | Set explicitly on `<dialog>` (see existing modals).                                    |
| `aria-labelledby`               | Set to the `id` of the `<h2>` title (e.g. `goal-modal-title`).                         |
| Focus trap                      | Native `<dialog>` + `showModal()` provides this — DO NOT re-implement.                 |
| Focus return on close           | Native `<dialog>` returns focus to opener — verified by browser. Add a vitest assert in §8.1 #1 to lock it. |
| ESC closes                      | Native `<dialog>` fires `cancel` then `close`. Custom dirty-check intercepts via `cancel` (§7.2). |
| `:focus-visible` outline        | Already global in `index.css:151-155` — applies to all interactive elements inside.    |
| Label association               | Every input/select/textarea has matching `<label htmlFor>` via `<FormField>`.          |
| Required indicator              | Visual `*` is `aria-hidden="true"`; required state is conveyed by validation message (`role="alert"`). Engineers MUST add `aria-required="true"` on required inputs (note: HTML `required` attribute is intentionally NOT used because we want custom validation messages, but `aria-required` still conveys the semantic). |
| Errors announced                | `<p role="alert">` per-field; global error region also has `role="alert"`. NEVER more than one `role="alert"` change per submit (collapse all field errors and the global into a single render pass). |
| `aria-describedby`              | Each input with an error or hint sets `aria-describedby={id+'-err'}` or `id+'-hint'`. `<FormField>` documents this contract; engineer MUST wire on each child input. |
| Color contrast                  | `--color-error` (#f44336) on `--color-bg-card` (#0d1f35) → contrast ~5.7:1 dark theme; #f44336 on #ffffff → 3.7:1 light theme. Light theme passes WCAG AA Large only. **Engineer MUST verify** light-theme error color and consider darkening to #b71c1c (contrast 6.4:1) — design will spec on follow-up if reviewer flags. |
| Touch target                    | Submit / Cancel buttons are `py-2` (~36px) wide × full row — meets 44×44 only on default font size + Cancel/Submit splitting half the modal width. Close button (X) is `w-8 h-8` = 32px — **below 44×44** but matches existing precedent. Document as known issue; do not change in UI-5 (would diverge from `CreateGoalModal` etc.). |
| Tab order                       | Title → … → Cancel → Submit. Native source order in JSX. Verify ProjectModal's button-group priority is reachable via Tab (`<button type="button">` is keyboard-focusable by default).|
| Status select keyboard          | Native `<select>` — keyboard support is built-in.                                      |
| Date input keyboard             | Native `<input type="date">` — keyboard support is built-in (varies by browser; acceptable). |
| Live char count                 | Caption beneath textarea is decorative; does NOT need `aria-live` (`maxLength` enforces hard cap). |

---

## 10. File layout

### 10.1 New files

| Path                                                 | Purpose                                       |
| ---------------------------------------------------- | --------------------------------------------- |
| `web/src/components/ui/FormField.tsx`                | Shared label + error/hint wrapper             |
| `web/src/components/ui/formStyles.ts`                | Shared input style + focus handlers           |
| `web/src/components/gtd/GoalModal.tsx`               | Renamed + extended `CreateGoalModal`; supports Create + Edit |
| `web/src/components/gtd/GoalModal.test.tsx`          | Vitest suite (§8.1)                           |
| `web/src/components/gtd/ProjectModal.tsx`            | Renamed + extended `CreateProjectModal`       |
| `web/src/components/gtd/ProjectModal.test.tsx`       | Vitest suite                                  |
| `web/src/components/decisions/DecisionModal.tsx`     | NEW — Create only                             |
| `web/src/components/decisions/DecisionModal.test.tsx`| Vitest suite                                  |

### 10.2 Modified files

| Path                                       | Change                                                                                |
| ------------------------------------------ | ------------------------------------------------------------------------------------- |
| `web/src/hooks/useGoals.ts`                | Add `useUpdateGoal` + `UpdateGoalRequest`; extend `CreateGoalRequest` with `status?`  |
| `web/src/hooks/useProjects.ts`             | Add `useUpdateProject` + `UpdateProjectRequest`; extend `CreateProjectRequest` with `status?` |
| `web/src/i18n/en.json`                     | Add `goals.modal.*`, `goals.status.*`, `projects.modal.*`, `decisions.modal.*`, `common.errors.*`, `common.submitting`, `common.charCount` |
| `web/src/i18n/zh-TW.json`                  | Mirror of en.json keys with translations from §5.5                                    |
| `web/src/pages/GtdPage.tsx`                | Update imports: `CreateGoalModal` → `GoalModal`, `CreateProjectModal` → `ProjectModal`; pass new props as needed |
| `web/src/pages/DecisionsPage.tsx`          | Add an "Add" header button (top-right of filter row) opening `DecisionModal`          |
| `web/src/pages/ProjectDetailPage.tsx`      | Add an "Edit" affordance opening `ProjectModal` with `entity={project}`; add a "Log decision" button opening `DecisionModal` with `project_id` pre-selected |
| `web/src/components/gtd/GoalCard.tsx`      | Add a small Edit affordance (icon button, top-right of card) opening `GoalModal` in Edit mode. State of `activeEditGoal` lives in `GtdPage.tsx`. |
| `web/src/components/dashboard/ProjectCard.tsx` (review only) | If reviewer wants Edit on Dashboard project cards, route the click through GtdPage state. **Not in scope unless reviewer requests** — flag in §11. |

### 10.3 Deleted files

| Path                                              | Reason                            |
| ------------------------------------------------- | --------------------------------- |
| `web/src/components/gtd/CreateGoalModal.tsx`      | Renamed to `GoalModal.tsx`        |
| `web/src/components/gtd/CreateProjectModal.tsx`   | Renamed to `ProjectModal.tsx`     |

> **MUST** check no other files import the deleted names. Confirmed via grep of repo: only `GtdPage.tsx` imports them.

### 10.4 No changes (explicit)

- `web/src/components/gtd/QuickAddModal.tsx` — out of scope (UI-5 covers Goal/Project/Decision, not Task).
- All other modals and components.

---

## 11. Open questions for Lead

| #  | Question                                                                                                         | Suggested default                                          |
| -- | ---------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------- |
| 1  | **Backend scope.** GoalEditModal + ProjectEditModal require new PATCH endpoints (see §6.7). Split UI-5 into two PRs (frontend Create-only first, Edit + backend later) OR ship backend in same PR? | Split — Create + Decision modals first (no backend risk), then a second PR with backend handlers + Edit modals. Cleaner review boundary. |
| 2  | **DecisionEditModal scope.** Decisions are append-only by domain semantics (`internal/decision/decision.go` ALL writes are `LogDecision`, never `Update*`). Spec excludes it. Confirm? | Confirm exclusion. If revision is needed, log a NEW decision that supersedes the old (existing pattern). |
| 3  | **Trigger surface for Goal Edit.** Edit affordance on `GoalCard` (small pencil-icon button top-right)? Or only via right-click menu / keyboard-only? | Visible icon button (matches industry standard, accessible). Engineer chooses lucide icon: `Pencil` size=14, top-right of card, `aria-label="Edit goal"`. |
| 4  | **Trigger surface for Project Edit.** Same question. Card icon? Or only on `ProjectDetailPage`? | Both. Add icon button on `ProjectCard` (variant="expanded" only, to avoid dashboard noise), and an "Edit" button in `ProjectDetailPage` header. |
| 5  | **`status` on Create.** Should Create modals expose `status` at all (default `active`), or hide it and force "create-as-active"? Edit needs status either way. | Hide on Create (cleaner first-time UX); always include in Edit. Spec is reversible — if Lead wants Create to expose status, the field is already designed and just needs uncommenting. |
| 6  | **Slug auto-suggest on Project Create.** Suggestion is opt-in click (per §4.4). Should it be auto-fill instead (with user able to override)? | Opt-in click. Auto-fill creates surprise + accidental commits to wrong slugs that are then immutable post-create. |
| 7  | **Light-theme error color contrast.** `#f44336` on `#ffffff` is ~3.7:1 — fails WCAG AA Normal text. Bump to `#b71c1c`? | Yes — bump in `index.css` for `html.light` `--color-error`. Out of UI-5 scope; flag for D1-style design follow-up. |
| 8  | **Close-button hit target.** 32×32 vs WCAG 44×44. Existing modals all use 32×32. Diverge or follow precedent? | Follow precedent for UI-5 (consistency > 12px). Document as known a11y debt; address project-wide in a separate UI cleanup PR. |
| 9  | **Goal `target_date` vs `due_date` field name.** API uses `due_date`, spec language uses "target date". Cosmetic only — keep label "Target date" + payload key `due_date`? Or rename API column? | Keep cosmetic split. API rename is breaking + out of scope. |
| 10 | **`status` server defaults.** Engineer MUST verify (read `internal/gtd/gtd.go`) whether `CreateGoal`/`CreateProject` default `status` to `active` server-side. If so, omit from Create payload. If not, include with default `active`. | Engineer reads source and adjusts. If unclear, default to including in payload. |

---

## 12. Out of scope (explicitly)

- Soft-delete / archive flow (covered by status='archived' + future Archive UI).
- Hard-delete (no DELETE endpoints exist; not requested).
- Bulk edit.
- Inline edit on cards (no double-click-to-edit).
- Undo toast for create/edit.
- Project task-count badge in modal.
- Goal-progress recalculation on edit.
- Right-click context menus.
- Drag-to-reorder.
- All modals are non-resizable; no drag-to-move.

---

## 13. Implementation order recommendation (for engineer)

1. Land `FormField.tsx` + `formStyles.ts` (no behavior change yet, just extraction; existing modals can stay as-is until step 4).
2. Add new i18n keys to both `en.json` and `zh-TW.json`. Verify build still passes (TS does not type-check JSON keys; verify by rendering each modal manually).
3. Create `DecisionModal.tsx` + tests + wire into `DecisionsPage.tsx` header. **Lowest risk** — no rename, no backend gating.
4. Rename `CreateGoalModal.tsx` → `GoalModal.tsx`; refactor to use `<FormField>`; add `entity?` prop and Edit mode (mutation guarded behind `useUpdateGoal` — if backend missing, disable submit + show TODO toast OR ship hook with backend per Lead's §11.1 decision).
5. Rename `CreateProjectModal.tsx` → `ProjectModal.tsx`; same refactor pattern.
6. Wire Edit affordances into `GoalCard`, `ProjectCard` (expanded variant), `ProjectDetailPage`.
7. `npm run lint && npm run build && npm test` MUST be 0-warnings before PR.
