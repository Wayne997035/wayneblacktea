# D5 — KnowledgePage: Pending Concept Proposals Section + Accept/Reject

**Task:** `1f854649`
**File touched:** `web/src/pages/KnowledgePage.tsx`
**New components:** `web/src/components/knowledge/PendingProposalsSection.tsx`, `web/src/components/knowledge/PendingProposalCard.tsx`, `web/src/hooks/usePendingProposals.ts`, `web/src/hooks/useResolveProposal.ts`

---

## 1. User journey

1. User opens `/knowledge` → sees the existing search bar + "Add entry" button + knowledge list.
2. **NEW:** Above the existing knowledge list (but below the search bar / add form), a **PendingProposalsSection** appears IF there are any `type=concept` pending proposals (auto-generated from previously added knowledge items, see `internal/proposal/autopropose.go`).
3. Section shows up to N (default 10) pending proposal cards, each with:
   - Proposed title (from `payload.title`),
   - First ~3 lines of content (truncated),
   - Tags (from `payload.tags`),
   - Source link ("from: TIL — Original article title", linkable to the source knowledge item),
   - **Accept** button (primary, blue) and **Reject** button (secondary, outline).
4. User clicks **Accept** → mutation calls `POST /api/proposals/:id/confirm` with `{ action: 'accept' }`. On success: card animates out, list refreshes, optional toast "Concept created — added to learning queue".
5. User clicks **Reject** → confirm modal "Are you sure?" → if yes, calls same endpoint with `{ action: 'reject' }`. Card removed.
6. If section is empty (no pending proposals), section is hidden entirely (no empty placeholder — keeps page clean).
7. Optional: show a "Pending proposals (3)" badge in the page header to draw attention even when section is collapsed by user.

---

## 2. Layout sketch

### Desktop (≥ 1024 px)

```
┌──────────────────────────────────────────────────────────────────────┐
│ Knowledge Base                                  [+ Add entry]        │
│ [🔍 Search…                ]                                          │
│                                                                      │
│ ┌──────────────────────────────────────────────────────────────────┐ │
│ │ PROPOSED CONCEPTS · 3 awaiting your review               [▾]    │ │
│ ├──────────────────────────────────────────────────────────────────┤ │
│ │ ┌──────────────────────────────────────────────────────────────┐ │ │
│ │ │ TIL→Concept                                                  │ │ │
│ │ │ Goroutines vs OS threads                                     │ │ │
│ │ │ A goroutine is a lightweight thread managed by the Go        │ │ │
│ │ │ runtime. Goroutines run in the same address space, so…       │ │ │
│ │ │ #go #concurrency · from: TIL "Goroutines basics"             │ │ │
│ │ │                              [Reject] [✓ Accept]             │ │ │
│ │ └──────────────────────────────────────────────────────────────┘ │ │
│ │ ┌──────────────────────────────────────────────────────────────┐ │ │
│ │ │ Article→Concept                                              │ │ │
│ │ │ React Server Components rendering model                      │ │ │
│ │ │ …                                                            │ │ │
│ │ │ #react #rsc · from: Article "RSC deep dive"                  │ │ │
│ │ │                              [Reject] [✓ Accept]             │ │ │
│ │ └──────────────────────────────────────────────────────────────┘ │ │
│ └──────────────────────────────────────────────────────────────────┘ │
│                                                                      │
│ ┌──────────────────────────────────────────────────────────────────┐ │
│ │ Existing KnowledgeCard list…                                     │ │
│ └──────────────────────────────────────────────────────────────────┘ │
└──────────────────────────────────────────────────────────────────────┘
```

### Mobile (< 640 px)

```
┌─────────────────────────────┐
│ Knowledge       [+ Add]     │
│ [🔍 Search…]                │
│                             │
│ PROPOSED CONCEPTS · 3 [▾]   │
│ ┌─────────────────────────┐ │
│ │ TIL→Concept             │ │
│ │ Goroutines vs OS threads│ │
│ │ A goroutine is a…       │ │
│ │ #go #concurrency        │ │
│ │ from: TIL "Goroutines…" │ │
│ │ [Reject]   [✓ Accept]   │ │
│ └─────────────────────────┘ │
│                             │
│ KnowledgeCard list…         │
└─────────────────────────────┘
```

Buttons full-width on very narrow screens (< 360 px); side-by-side on ≥ 360 px.

---

## 3. Component tree

```
KnowledgePage (existing) — MODIFY: insert PendingProposalsSection above the list
├── header (existing)
├── search bar (existing)
├── add form (existing)
├── PendingProposalsSection                   ← NEW
│   ├── section header (label + count + collapse chevron)
│   └── list of PendingProposalCard
│       ├── source-type badge (e.g. "TIL→Concept")
│       ├── title
│       ├── content (truncated 3 lines)
│       ├── tags row
│       ├── source attribution
│       └── action buttons (Accept primary / Reject secondary)
└── existing KnowledgeCard list
```

**New components:**
- `PendingProposalsSection.tsx` — calls `usePendingProposals()`. Hides itself when `data.length === 0`. Manages local `collapsed` state (default expanded).
- `PendingProposalCard.tsx` — props: `proposal: PendingProposal`, `onAccept: (id: string) => void`, `onReject: (id: string) => void`.
- `usePendingProposals.ts` — `useQuery` against `GET /api/proposals/pending`.
- `useResolveProposal.ts` — `useMutation` against `POST /api/proposals/:id/confirm`. On success, invalidate `['proposals', 'pending']` and `['knowledge']`.

**Modify:**
- `KnowledgePage.tsx` — import & render `<PendingProposalsSection />` above `{items.map(...)}` block.

**Reuse:**
- Existing toast pattern (if `toastStore.ts` is wired) — show success toast on accept/reject. If toast is not yet integrated into pages, skip and rely on optimistic UI removal.
- `LoadingSkeleton`, `EmptyState`.

---

## 4. State / data shape

### Frontend types (NEW, add to `web/src/types/api.ts`)

```ts
export type ProposalType = 'goal' | 'project' | 'task' | 'concept'
export type ProposalStatus = 'pending' | 'accepted' | 'rejected'

export interface ConceptCandidatePayload {
  title: string
  content: string
  tags?: string[]
  source_item_id?: string       // knowledge_items.id
  source_item_type?: string     // 'article' / 'til' / 'zettelkasten'
}

export interface PendingProposal {
  id: string
  type: ProposalType
  status: ProposalStatus
  payload: ConceptCandidatePayload    // discriminated by `type`; D5 only renders `concept`
  proposed_by: string | null
  created_at: string
  resolved_at: string | null
}

export interface ResolveProposalRequest {
  action: 'accept' | 'reject'
}
```

### Hooks

```ts
export function usePendingProposals() {
  return useQuery<PendingProposal[]>({
    queryKey: ['proposals', 'pending'],
    queryFn: () => apiFetch<PendingProposal[]>('/api/proposals/pending?type=concept'),
    staleTime: 60_000,
  })
}

export function useResolveProposal() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, action }: { id: string; action: 'accept' | 'reject' }) =>
      apiFetch<PendingProposal>(`/api/proposals/${id}/confirm`, {
        method: 'POST',
        body: JSON.stringify({ action }),
      }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ['proposals', 'pending'] })
      void qc.invalidateQueries({ queryKey: ['knowledge'] })
    },
  })
}
```

### Local state in PendingProposalsSection

```ts
const [collapsed, setCollapsed] = useState(false)
const [confirmReject, setConfirmReject] = useState<string | null>(null) // proposal id pending reject confirmation
const { data: proposals = [], isLoading, isError } = usePendingProposals()
const resolveMutation = useResolveProposal()
```

---

## 5. Edge cases

| State | Render |
|-------|--------|
| `isLoading` | Show 2 × `LoadingSkeleton h-32 w-full` inside the section card. Section header still shown with "loading…" placeholder count. |
| `isError` | Section header + inline red error block "Could not load proposals · Retry"; "Retry" button calls `refetch()`. |
| `proposals.length === 0` | Hide entire section (do NOT render empty placeholder — page is cleaner without it). |
| Section collapsed (user clicked chevron) | Header stays visible; list hidden. State NOT persisted across reloads (acceptable for MVP). |
| Mutation in flight | The clicked button shows spinner; both buttons disabled; card has `opacity-70 pointer-events-none`. |
| Mutation success (accept) | Card animates out (fade + slide-up 200ms), list refreshes (TanStack invalidate); optional toast "Concept added to learning queue". |
| Mutation success (reject) | Same animation; toast "Proposal rejected". |
| Mutation error | Card returns to normal; inline `text-error` row below the card: "Failed to resolve · Try again"; buttons re-enabled. |
| `payload.source_item_id` null | Show "from: agent-proposed" instead of clickable source link. |
| `payload.source_item_id` valid but knowledge item deleted | Source line shows greyed out "from: (deleted)". (Optional — skip this check for MVP, just render the linked text.) |
| Reject confirmation modal | Use a `<dialog>` element (HTML native, matches existing pattern in `index.css:135`). Title "Reject proposal?" + body + Cancel / Reject buttons. |
| User has 50+ pending proposals | Show first 10, then "Show all (50)" link → expands list inline (no pagination needed). |

---

## 6. Backend contract

### ⚠️ NEW endpoint: `GET /api/proposals/pending`

- **Method:** GET
- **Path:** `/api/proposals/pending`
- **Query params:** `?type=concept` (optional filter; D5 always passes `type=concept`)
- **Auth:** X-API-Key header (same as other `/api/*`)
- **Response 200:** `PendingProposal[]` (sorted newest-first)
  ```json
  [
    {
      "id": "8a1f4c2d-...-...",
      "type": "concept",
      "status": "pending",
      "payload": {
        "title": "Goroutines vs OS threads",
        "content": "A goroutine is a lightweight thread…",
        "tags": ["go", "concurrency"],
        "source_item_id": "b2e7f1a4-...",
        "source_item_type": "til"
      },
      "proposed_by": "claude-code",
      "created_at": "2026-04-28T10:15:00Z",
      "resolved_at": null
    }
  ]
  ```

**Backend implementation hint** (out of frontend scope, included for context): wire to `proposal.Store.ListPending(ctx)` already used by MCP tool `list_pending_proposals` (`internal/mcp/tools_proposal.go:75`). Optionally filter by `type` server-side; client also filters defensively.

### ⚠️ NEW endpoint: `POST /api/proposals/:id/confirm`

- **Method:** POST
- **Path:** `/api/proposals/:id/confirm`
- **Auth:** X-API-Key header
- **Request body:** `{ "action": "accept" | "reject" }`
- **Response 200 (accept):**
  ```json
  {
    "proposal": { "id": "…", "status": "accepted", "resolved_at": "2026-04-28T14:32:07Z", … },
    "concept": { "id": "…", "title": "…", "content": "…", "tags": […] }
  }
  ```
  (For frontend purposes, `concept` is optional; we just need to know it succeeded so we can invalidate.)
- **Response 200 (reject):**
  ```json
  { "proposal": { "id": "…", "status": "rejected", "resolved_at": "2026-04-28T14:32:07Z", … } }
  ```
- **Response 404:** proposal not found or already resolved (idempotent).
- **Response 400:** invalid action.

**Backend implementation hint** (out of frontend scope): wire to `proposal.Store.Resolve(ctx, id, status)` already used by MCP tool `confirm_proposal` (`internal/mcp/tools_proposal.go:79`). For `accept`, the MCP version atomically materialises the entity (e.g. creates the concept row) — replicate that behaviour in the HTTP handler.

### Existing endpoints (no change)

`GET /api/knowledge`, `GET /api/knowledge/search`, `POST /api/knowledge` — unchanged.

---

## 7. Tailwind class palette

| Purpose | Class / token |
|---------|--------------|
| Section wrapper | `rounded-lg p-5 mb-6` + `bg-[var(--color-bg-card)]` + `border border-[var(--color-border)]` |
| Section header | `flex items-center justify-between mb-3` |
| Section label | `text-label` + `var(--color-text-muted)` |
| Pending count badge | `text-label rounded-full px-2 py-0.5` + `bg-[var(--color-status-on-hold-bg)]` + `var(--color-status-on-hold-text)` |
| Collapse chevron button | `rounded p-1` + `var(--color-text-muted)` + `hover:bg-[var(--color-bg-hover)]` |
| ProposalCard | `rounded-md p-4 mb-3` + `bg-[var(--color-bg-input)]` + `border border-[var(--color-border)]` |
| Source-type badge | `text-label rounded px-2 py-0.5` + `bg-[var(--color-bg-hover)]` + `var(--color-accent-blue)` + `border border-[var(--color-border)]` |
| Proposal title | `text-card-title mb-1` + `var(--color-text-primary)` |
| Proposal content (3-line clamp) | `text-body-sm mb-2` + `var(--color-text-muted)` + `display: -webkit-box; -webkit-line-clamp: 3; -webkit-box-orient: vertical; overflow: hidden;` |
| Tag chip | `text-label rounded-full px-2 py-0.5` + `bg-[var(--color-bg-hover)]` + `var(--color-text-muted)` |
| Source attribution row | `text-caption flex items-center gap-2` + `var(--color-text-muted)` |
| Actions row | `flex justify-end gap-2 mt-3` |
| Accept button (primary) | `rounded-md px-4 py-2 text-body-sm` + `bg-[var(--color-accent-blue)]` + `text-[var(--color-bg-base)]` + `hover:opacity-90` |
| Reject button (secondary) | `rounded-md px-4 py-2 text-body-sm` + `border border-[var(--color-border)]` + `var(--color-text-muted)` + `hover:bg-[var(--color-bg-hover)]` |
| Disabled state | `opacity-60 cursor-not-allowed` |
| Reject confirm dialog | reuse `<dialog>` pattern (already styled in `index.css:135`) |

---

## 8. Accessibility

- Section: `<section aria-labelledby="proposals-heading">` with `<h2 id="proposals-heading" className="text-label">Proposed concepts</h2>` so screen readers announce the section.
- Pending count: include in heading text "Proposed concepts · 3 awaiting your review" (no separate aria-label needed).
- Collapse chevron: `<button aria-expanded={!collapsed} aria-controls="proposals-list">` with `aria-label="Collapse proposals section"` / "Expand…" toggle.
- Each ProposalCard: `<article aria-labelledby={titleId}>`. Title is `<h3 id={titleId}>`.
- Action buttons: text labels (no icon-only). `Accept` and `Reject` buttons need clear text — do NOT rely on icon alone. Optional small icon to the left of text.
- Mutation in-flight: `<button aria-busy="true">` + spinner; screen reader announces busy state.
- Reject confirmation dialog: `<dialog>` with `aria-labelledby` + `aria-describedby`. Trap focus inside dialog; `Esc` closes. Default focus on Cancel (safe default — destructive action requires explicit second confirmation).
- Tag chips and source attribution are plain text — no extra aria needed.
- Animation on accept/reject: respect `prefers-reduced-motion: reduce` — instant remove, no fade/slide.
- Live region: when proposals are accepted/rejected, announce result via `aria-live="polite"` (e.g. "Proposal accepted. 2 remaining.").

---

## 9. Acceptance criteria

- [ ] `usePendingProposals()` hook calls ⚠️ NEW `GET /api/proposals/pending?type=concept` and returns typed `PendingProposal[]`.
- [ ] `useResolveProposal()` hook calls ⚠️ NEW `POST /api/proposals/:id/confirm` with `{ action }` body; on success invalidates both `['proposals','pending']` and `['knowledge']` query caches.
- [ ] `PendingProposalsSection` renders above the existing knowledge list when `proposals.length > 0`; hidden entirely when 0 (no empty placeholder).
- [ ] Section header shows label + count badge + collapse chevron; chevron toggles list visibility (state not persisted across reloads — acceptable).
- [ ] Each `PendingProposalCard` shows: source-type badge ("TIL→Concept"), title, 3-line clamped content, tag chips, source attribution, Accept and Reject buttons.
- [ ] Clicking **Accept** triggers mutation; while in-flight, both buttons disabled and spinner shown; on success, card animates out and list refreshes.
- [ ] Clicking **Reject** opens a `<dialog>` confirmation; only after Cancel/Confirm choice does the mutation fire (Cancel keeps card unchanged).
- [ ] Mutation error: card un-disables; inline error message below card with "Try again" implicit by re-clicking.
- [ ] Loading state: 2 × `LoadingSkeleton` placeholders within section card.
- [ ] Error state: inline red banner with retry; section header still visible.
- [ ] Mobile (< 640 px): cards stack full-width; Accept/Reject buttons side-by-side ≥ 360 px, full-width below; no horizontal overflow.
- [ ] `prefers-reduced-motion: reduce` → cards remove instantly without animation.
- [ ] All buttons have visible text labels (Accept / Reject); icons optional.
- [ ] `npm run lint` and `npm run build` exit clean.
