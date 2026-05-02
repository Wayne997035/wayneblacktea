# wayneblacktea MCP Tools Reference

The MCP server (`cmd/mcp`) connects Claude Code to wayneblacktea via `.mcp.json`.

---

## Permissions matrix

| Tool | Domain | R/W | Significant? | Confirm gate? |
|------|--------|-----|--------------|---------------|
| `initial_instructions` | Onboarding | R | No | No |
| `get_today_context` | Context | R | No | No |
| `list_active_repos` | Context | R | No | No |
| `sync_repo` | Context | W | Yes | No |
| `list_projects` | GTD | R | No | No |
| `create_project` | GTD | W | No | No |
| `get_project` | GTD | R | No | No |
| `update_project_status` | GTD | W | Yes | No |
| `list_tasks` | GTD | R | No | No |
| `add_task` | GTD | W | No | No |
| `update_task` | GTD | W | No | No |
| `complete_task` | GTD | W | Yes | No |
| `delete_task` | GTD | W | No | No |
| `list_goals` | GTD | R | No | No |
| `create_goal` | GTD | W | No | No |
| `log_activity` | GTD | W | No | No |
| `log_decision` | Decision | W | No | No |
| `list_decisions` | Decision | R | No | No |
| `add_knowledge` | Knowledge | W | No | No |
| `search_knowledge` | Knowledge | R | No | No |
| `list_knowledge` | Knowledge | R | No | No |
| `sync_to_notion` | Knowledge | W | No | No |
| `get_due_reviews` | Learning | R | No | No |
| `submit_review` | Learning | W | No | No |
| `create_concept` | Learning | W | No | No |
| `set_session_handoff` | Session | W | No | No |
| `resolve_handoff` | Session | W | Yes | No |
| `propose_goal` | Proposal | W | No | Yes |
| `propose_project` | Proposal | W | No | Yes |
| `list_pending_proposals` | Proposal | R | No | No |
| `confirm_proposal` | Proposal | W | Yes | User decides |
| `confirm_plan` | Plan | W | No | No |
| `system_health` | Health | R | No | No |
| `upsert_project_arch` | Arch | W | Yes | No |
| `get_project_arch` | Arch | R | No | No |

**Significant:** triggers background classify middleware (auto-log implicit decisions/tasks, rate-limited to 60/window).
**Confirm gate:** `propose_*` creates a pending proposal not materialised until `confirm_proposal(action=accept)`.

---

## Tool details

### `initial_instructions`
Returns the full usage protocol. No args. Call after `get_today_context` at session start.

---

### `get_today_context`
**Call at session start.** Returns active goals, projects, weekly progress, pending handoff, primary arch snapshot. No args.

---

### `list_active_repos` / `list_projects` / `list_goals`
Read-only, no args. Return JSON arrays.

### `list_tasks`
Optional `project_id` (UUID) filter.

### `list_decisions`
Optional: `repo_name` (string), `project_id` (UUID), `limit` (default 20). Call before scanning code.

### `list_knowledge`
Optional: `limit` (default 20), `offset`.

### `list_pending_proposals`
No args. Newest first.

### `get_due_reviews`
No args. Returns up to 50 due concepts with FSRS state fields.

### `system_health`
Optional: `recent_calls` (default 20), `stuck_threshold_hours` (default 4). Returns counts, stuck tasks, forgotten signals.

---

### `sync_repo`

| Arg | Required |
|-----|----------|
| `name` | Yes — unique repo name |
| `path` `description` `language` `current_branch` `next_planned_step` | No |

---

### `create_project`

| Arg | Required |
|-----|----------|
| `name` (slug) `title` `area` | Yes |
| `description` `goal_id` (UUID) `priority` (1–5) | No |

### `get_project`

`name` (project slug) required. Returns `{project, recent_decisions}`.

### `update_project_status`

`project_id` (UUID) and `status` (`active` `completed` `archived` `on_hold`) required. **Significant.**

---

### `add_task`

Call immediately when follow-up work is identified.

| Arg | Required |
|-----|----------|
| `title` | Yes |
| `project_id` (UUID) `priority` (1–5) `importance` (1–3) `assignee` `context` `description` | No |

### `update_task`

`task_id` (UUID) and `status` (`pending` `in_progress` `cancelled`) required.

### `complete_task`

`task_id` (UUID) required. Optional `artifact` (PR URL / SHA). **Significant.**

> "Call `complete_task` with task_id=TASK_UUID, artifact='https://github.com/.../pull/42'."

### `delete_task`

`task_id` (UUID) required. Permanent.

---

### `create_goal`

`title` and `area` required. Optional `description`, `due_date` (RFC3339).

### `log_activity`

`actor` and `action` required. Optional `project_id`, `notes`.

---

### `log_decision`

Call when a decision is confirmed (user says go / 好).

`title`, `context`, `decision`, `rationale` required.
Optional: `repo_name`, `project_id`, `alternatives`.

> "Call `log_decision` with title='Use FSRS', context='...', decision='Adopted FSRS', rationale='...'."

---

### `add_knowledge`

`type` (`article` `til` `bookmark` `zettelkasten`) and `title` required.
Optional: `content`, `url`, `tags` (comma-separated).

Auto-proposes a concept card; response includes `concept_proposal_id` when proposed.

> "Call `add_knowledge` with type=til, title='Go defer runs after return value is set'."

### `search_knowledge`

`query` required. Optional `limit` (default 10). Full-text + vector similarity. Call before fetching a URL.

### `sync_to_notion`

`knowledge_id` (UUID) required. Requires `NOTION_INTEGRATION_SECRET`. Returns Notion page URL.

---

### `submit_review`

`schedule_id` (UUID) and `rating` (1–4: Again/Hard/Good/Easy) required.
Pass back `stability`, `difficulty`, `review_count` from `get_due_reviews`.

### `create_concept`

`title` and `content` required. Optional `tags` (comma-separated). Initialises FSRS schedule.

---

### `set_session_handoff`

Call when user says tomorrow / later. `intent` required. Optional `repo_name`, `context_summary`, `project_id`.

### `resolve_handoff`

`handoff_id` (UUID) required. Call at session start after reading the pending handoff. **Significant.**

---

### `propose_goal`

`title` and `area` required. Optional `description`, `due_date`, `proposed_by`.

### `propose_project`

`name`, `title`, `area` required. Optional `description`, `goal_id`, `priority`, `proposed_by`.

### `confirm_proposal`

`proposal_id` (UUID) and `action` (`accept` / `reject`) required. **Significant.** `accept` materialises the entity atomically (Postgres: in a single transaction).

> "Call `list_pending_proposals`, then `confirm_proposal` with action=accept."

---

### `confirm_plan`

Call when user confirms a plan ("可以" "好" "go" "開始"). Atomically creates tasks + logs decisions.

`phases` (JSON array, required): `[{"title":"...","description":"...","priority":2}]`

Optional `decisions` (JSON array): `[{"title":"...","context":"...","decision":"...","rationale":"..."}]`,
`project_id`, `repo_name`.

> "Call `confirm_plan` with phases=[{\"title\":\"Write api.md\",\"priority\":1}]."

---

### `upsert_project_arch`

Call after reading 3+ `internal/` files. **Significant.**

`slug` (repo name) and `summary` (max 8000 chars) required.
Optional `file_map` (JSON object, max 128 KB), `last_commit_sha`.

### `get_project_arch`

`slug` required. Compare `last_commit_sha` with `git rev-parse HEAD` to check staleness.

---

## Automatic trigger mechanisms

**Classify middleware:** After each significant tool call, a background goroutine may auto-log implicit decisions or follow-up tasks (AI-powered, rate-limited to 60 calls/rolling window).

**Stop hook:** `scripts/wbt-stop-hook.sh` calls `POST /api/auto-handoff` and `POST /api/activity` when Claude Code exits.

**Session start:** `UserPromptSubmit` hook calls `get_today_context` automatically at the start of each conversation.
