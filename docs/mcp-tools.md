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
| `get_task` | GTD | R | No | No |
| `set_task_status` | GTD | W | No | No |
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
| `add_vision_item` | Vision | W | No | No |
| `list_vision_items` | Vision | R | No | No |
| `update_vision_item` | Vision | W | No | No |
| `promote_vision_to_task` | Vision + GTD | W | No | No |
| `navigate_knowledge` | Knowledge | R | No | No |
| `outline_knowledge` | Knowledge | R | No | No |
| `update_project` | GTD | W | No | No |
| `begin_task` | GTD | W | No | No |
| `get_upcoming_work` | GTD | R | No | No |
| `task_checklist_add_item` | GTD | W | No | No |
| `task_checklist_toggle` | GTD | W | No | No |
| `task_checklist_complete` | GTD | W | No | No |
| `mark_next_action_done` | Session | W | No | No |
| `confirm_proposals` | Proposal | W | No | User decides |
| `propose_behavior_rule` | Behavior Rule | W | No | No |
| `list_behavior_rules` | Behavior Rule | R | No | No |
| `apply_behavior_rules` | Behavior Rule | W | No | No |
| `deprecate_behavior_rule` | Behavior Rule | W | No | No |
| `generate_reflection` | Reflection | W | No | No |
| `list_reflections` | Reflection | R | No | No |
| `get_latest_reflection` | Reflection | R | No | No |
| `analyze_recent_patterns` | Reflection | R | No | No |
| `analyze_agent_behavior` | Watchdog | W | No | No |
| `detect_unclosed_loops` | Watchdog | R | No | No |
| `mark_loop_resolved` | Watchdog | W | No | No |
| `start_work` | Work Session | W | No | No |
| `get_active_work` | Work Session | R | No | No |
| `checkpoint_work` | Work Session | W | No | No |
| `finish_work` | Work Session | W | No | No |
| `closeout_session_check` | Closeout | W | No | No |
| `detect_completion_candidates` | Dashboard | W | No | No |
| `reconcile_dashboard` | Dashboard | W | No | No |
| `record_outcome` | Outcome | W | No | No |
| `evaluate_outcome` | Outcome | W | No | No |
| `list_recent_outcomes` | Outcome | R | No | No |
| `find_failed_patterns` | Outcome | R | No | No |
| `extract_skill` | Skill | W | No | No |
| `search_skills` | Skill | R | No | No |
| `use_skill` | Skill | W | No | No |
| `update_skill_from_outcome` | Skill | W | No | No |
| `list_relevant_skills` | Skill | R | No | No |
| `generate_project_status` | Status | R/W | No | No |
| `promote_atom_to_knowledge` | Atom | W | No | Yes |
| `traverse_atoms` | Atom | R | No | No |
| `search_atoms` | Atom | R | No | No |
| `reconcile_merged_prs` | Reconcile | W | No | No |
| `list_playbooks` | Playbook | R | No | No |
| `add_procedural` | Procedural | W | No | No |
| `query_procedural` | Procedural | R | No | No |
| `mark_procedural_used` | Procedural | W | No | No |
| `recall` | Procedural | R | No | No |

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
| `description` `goal_id` (UUID) `priority` (1–5) `repo_name` (VCS repo slug to link, e.g. `wayneblacktea`) | No |

### `get_project`

`name` (project slug) required. Returns `{project, recent_decisions}`.

### `update_project_status`

`project_id` (UUID) and `status` (`active` `completed` `archived` `on_hold`) required. **Significant.**

---

### `add_task`

Call immediately when follow-up work is identified.

| Arg | Required |
|-----|----------|
| `title` `due_date` (RFC3339, e.g. `2026-12-31T00:00:00Z`) | Yes |
| `project_id` (UUID) `priority` (1–5) `importance` (1–3) `assignee` `context` `description` `kind` (`general`/`fix-pr`/`feature`/`refactor`/`research`/`chore`, default `general`) `branch_name` `pr_url` | No |

### `update_task`

Updates one or more mutable fields of a task. All params except `task_id` are optional (at least one must be set). Use `complete_task` to mark a task completed.

| Arg | Required |
|-----|----------|
| `task_id` | Yes — Task UUID |
| `status` (`pending`/`in_progress`/`cancelled`) `title` (max 2000) `description` (max 10000) `priority` (1-5) `importance` (1-3) `assignee` (max 200) `due_date` (RFC3339) `context` (max 10000) `branch_name` (empty string clears) `pr_url` (empty string clears) | No |

### `complete_task`

`task_id` (UUID) required. Optional `artifact` (PR URL / SHA). **Significant.**

> "Call `complete_task` with task_id=TASK_UUID, artifact='https://github.com/.../pull/42'."

### `delete_task`

Permanently deletes a task. TWO-STEP: first call with only `task_id` returns `{deletion_token, expires_at}`; second call MUST include `confirm=true` and `deletion_token` to perform the delete. Tokens expire after 60s.

| Arg | Required |
|-----|----------|
| `task_id` | Yes — Task UUID |
| `confirm` `deletion_token` | No — required together on the second (confirming) call |

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

### `add_vision_item`

Call when user describes something conceptually important but currently blocked. Persists as a Vision item with status `open`.

| Arg | Required |
|-----|----------|
| `title` | Yes |
| `why_blocked` | Yes — what is blocking it |
| `depends_on` | No — JSON array of strings (e.g. `'["task-abc"]'`) |
| `parent_initiative` | No — broader roadmap or initiative name |
| `context_md` | No — additional markdown notes |
| `repo_name` | No — repository or project slug |

### `list_vision_items`

Optional `status` filter (`open` `discussing` `maturing` `promoted` `dismissed`; default: all non-dismissed).
Optional `parent_initiative` filter. Returns summary view (no `context_md`).

### `update_vision_item`

`id` (UUID) required. All other args optional.

| Arg | Notes |
|-----|-------|
| `status` | `open` `discussing` `maturing` `promoted` `dismissed` |
| `context_md` | Replaces existing markdown notes |
| `last_discussed_at` | RFC3339; defaults to NOW() if omitted when called |

If `last_discussed_at` is not supplied the server sets it to the current time automatically.

### `promote_vision_to_task`

Atomically creates a GTD task and marks the vision item `status=promoted`. **Significant.**

`id` (vision item UUID) required.

| Arg | Notes |
|-----|-------|
| `title` | GTD task title; defaults to vision item title if omitted |
| `description` | GTD task description |
| `priority` | 1–5 (lower = higher priority). Default: 3 |

Returns `{task, vision_item}`.

---

### `navigate_knowledge`

Browses the knowledge tree. With no arguments returns all root-level items. Supply `parent_id` (UUID) to list direct children (section headings of a document). Returns lightweight metadata only — no content.

Optional `parent_id` (UUID).

### `outline_knowledge`

Returns the full heading tree for a root knowledge document — useful as a table of contents before choosing which section to read. Returns no content, only headings ordered by level.

`item_id` (UUID of the root knowledge item) required.

---

### `update_project`

Updates mutable fields of a project. All params except project_id are optional. Omitted params preserve the existing value. Use `update_project_status` to change status only.

| Arg | Required |
|-----|----------|
| `project_id` | Yes — Project UUID |
| `title` (max 500) `description` (max 5000) `area` `priority` (1-5) `status` (`active`/`completed`/`archived`/`on_hold`) `goal_id` (empty string clears the link) `repo_name` (empty string clears the link) | No |

### `begin_task`

Atomically marks a task in_progress, logs a `work_session_started` activity, and returns the task with a `branch_name_suggestion` and `work_session_id`. Call this INSTEAD of `update_task` when starting work on a task. Optionally pass `branch_name` and/or `pr_url` to persist the linkage so `reconcile_merged_prs` can auto-close the task on PR merge.

| Arg | Required |
|-----|----------|
| `task_id` | Yes — Task UUID |
| `branch_name` | No — git branch name to persist on the task |
| `pr_url` | No — GitHub PR URL to persist on the task |

### `get_upcoming_work`

Returns pending/in-progress tasks grouped into today, tomorrow, day_after, upcoming, and unscheduled_important buckets. Use to plan the next work session or surface high-importance tasks that have no due date.

Optional `days` (1-14, default 7).

### `task_checklist_add_item`

Appends a new checklist item to a task. Returns the full updated checklist. Use to track sub-steps or acceptance criteria for a task.

`task_id` and `title` (max 500 chars) required. Optional `file_ref` (max 2000 chars), `notes` (max 2000 chars).

### `task_checklist_toggle`

Partially updates a checklist item (done flag, title, notes, evidence_url). Returns the full updated checklist.

`task_id`, `item_id`, and `done` (boolean) required. Optional `evidence_url` (max 2000 chars) — URL or note proving the item is done.

### `task_checklist_complete`

Shorthand for marking a checklist item `done=true` and recording `completed_at=now`. Returns the full updated checklist.

`task_id` and `item_id` required.

### `get_task`

Returns a single task by UUID. Status-agnostic — retrieves pending, in_progress, completed, and cancelled tasks. Use `list_tasks` for filtered bulk retrieval.

`task_id` (UUID) required.

### `set_task_status`

Transitions a task to a new status, including reopen (completed/cancelled → pending/in_progress). Same-to-same status is an idempotent no-op. Allowed transitions: pending↔in_progress, pending→completed/cancelled, in_progress→completed/cancelled, completed/cancelled→pending/in_progress/completed/cancelled. IMPORTANT: this tool MUST NOT call `record_outcome` or `evaluate_outcome` — outcome recording stays exclusively in those tools. Reopen→re-complete records no outcome.

`task_id` (UUID) and `status` (`pending` `in_progress` `completed` `cancelled`) required.

---

### `mark_next_action_done`

Sets `next_actions[step].status = 'done'` for the given handoff. Only works on handoffs that belong to the caller's workspace.

`handoff_id` and `step` (integer) required.

---

### `confirm_proposals`

Batch accept or reject multiple pending proposals in a single call. Accepts 1–100 proposal UUIDs and a single action (`accept` or `reject`). On Postgres the batch is atomic — any single failure rolls back all. On SQLite each proposal is processed independently (best-effort).

`ids` (array of 1–100 proposal UUID strings) and `action` (`accept` or `reject`) required.

> Note: unlike `confirm_proposal` (singular), `confirm_proposals` is not in the classify-middleware `significantTools` set in `internal/mcp/middleware_classify.go`, so it is not auto-logged even though it performs the same accept/reject materialisation.

---

### `propose_behavior_rule`

Proposes a new behavior rule with `status='proposed'`. The rule enters the proposal queue and must be promoted via `apply_behavior_rules` (`outcome='success'`) before it becomes `'active'`. Use to encode lessons from reflections or outcomes as actionable rules.

| Arg | Required |
|-----|----------|
| `condition` | Yes — when-clause describing the condition that triggers this rule |
| `action` | Yes — what action to take when the condition is met |
| `source_type` | Yes — one of: reflection, outcome, manual |
| `source_id` | No — UUID of the source entity (reflection or outcome ID) |
| `confidence` | No — initial confidence 0.0–1.0, defaults to 0.50 when absent or out of range |

> Note: this does NOT create a `pending_proposals` row like `propose_goal`/`propose_project` — it inserts directly into the `behavior_rules` table with `status='proposed'`. Its confirm gate is `apply_behavior_rules`, not `confirm_proposal`.

### `list_behavior_rules`

Lists persisted behavior rules, ordered by creation time descending. Use status filter to view only proposed/active/rejected/deprecated rules.

Optional `status` (`proposed`/`active`/`rejected`/`deprecated`; empty = all). Optional `limit` (default 20, max 100).

### `apply_behavior_rules`

Applies an outcome to a behavior rule, adjusting confidence and conditionally transitioning status. `outcome='success'` on a proposed rule transitions it to `'active'` and increments confidence by 0.05 (capped at 1.00). `outcome='failure'` decrements confidence by 0.10 (floored at 0.00) without changing status.

`rule_id` and `outcome` (`success` or `failure`) required.

### `deprecate_behavior_rule`

Sets a behavior rule's status to `'deprecated'`. Idempotent: already-deprecated rules return success without error. Deprecated rules are retained for 365 days then pruned by the daily TTL job.

`rule_id` required.

---

### `generate_reflection`

Persists an AI-generated reflection record. Does NOT call an LLM — the caller provides the already-generated content. Use to record daily/weekly reviews, task post-mortems, decision retrospectives, and system-level pattern observations.

| Arg | Required |
|-----|----------|
| `type` | Yes — one of: daily, weekly, task, decision, proposal, knowledge, system |
| `summary` (max 5000 chars) `insights` (JSON) `patterns_detected` (JSON) `suggested_actions` (JSON) `confidence` (0.0–1.0, default 0.0) `related_entity_type` `related_entity_id` (UUID) | No |

### `list_reflections`

Lists persisted reflection records, ordered by creation time descending.

Optional `type` filter (same enum as `generate_reflection`). Optional `limit` (default 20).

### `get_latest_reflection`

Returns the most recently created reflection of a given type, or null when none exists yet.

`type` required.

### `analyze_recent_patterns`

Returns recent reflection records that contain detected patterns. Useful for surfacing recurring themes across the last N days.

Optional `days` (default 7).

---

### `analyze_agent_behavior`

Runs 8 persisted + 1 live self-monitoring detections against live store data (stuck tasks, unlogged decisions, stale handoffs, task_missing_due_date, etc.). Inserts persisted findings into the `discipline_events_m8` table and returns a JSON summary with `findings` and `live_findings` (the 9th detection, task_missing_due_date, is live/non-persisted).

Optional `stuck_threshold_hours` (default 4) — tasks in_progress longer than this are flagged stuck.

### `detect_unclosed_loops`

Returns all open `discipline_events_m8` rows (`resolved_at IS NULL`) scoped to the current workspace. Each entry is a self-monitoring signal that has not yet been acknowledged.

No args.

### `mark_loop_resolved`

Marks a discipline event as resolved by its UUID. Call this after you have addressed the underlying issue surfaced by `analyze_agent_behavior`.

`event_id` required.

---

### `start_work`

Start a new work session for a repository. Call this when beginning focused work on a repo that doesn't already have an active session. Links the supplied task_ids as primary tasks and sets current_task_id to the first one.

| Arg | Required |
|-----|----------|
| `repo_name` `title` (max 200) `goal` (max 2000) | Yes |
| `task_ids` (JSON array, max 50 UUIDs) `project_id` `source` (`manual`/`confirm_plan`/`hook`/`other`, default `manual`) | No |

### `get_active_work`

Get the current in_progress work session for a repository. Returns `{active:false}` when no session exists. Check `implementation_allowed` before editing code.

`repo_name` required.

### `checkpoint_work`

Save progress on the current work session without ending it. Sets `status=checkpointed` and records `last_checkpoint_at`. Use when taking a break or switching context temporarily.

| Arg | Required |
|-----|----------|
| `session_id` `summary` (max 5000 chars) | Yes |
| `completed_task_ids` `new_task_titles` `new_decisions` `blockers` `next_actions` (all JSON arrays) | No |

### `finish_work`

Close the current work session as completed. Sets `status=completed`, records `completed_at` and `final_summary`. Always call this when work on a session is done, even if tasks remain.

| Arg | Required |
|-----|----------|
| `session_id` `summary` (max 5000 chars) | Yes |
| `completed_task_ids` `deferred_task_ids` `follow_up_tasks` `new_decisions` (JSON arrays) `artifact` | No |

---

### `closeout_session_check`

Aggregates session-end checks into one actionable closeout report: open in_progress tasks, stuck tasks (in_progress > 7 days), pending proposals awaiting user resolution, latest session handoff status, and completion candidates. Also writes a summary line to `activity_log`. Call at session end to verify nothing was left open.

No args. Read-heavy aggregation that also performs one best-effort `activity_log` write per call (`_ = s.gtd.LogActivity(...)` — errors ignored).

---

### `detect_completion_candidates`

Scans tasks and activity_log to surface tasks that appear done but GTD status is still pending/in_progress. Read-only for tasks — writes to `completion_candidates` table only. Returns candidate list.

Optional `stale_threshold_hours` (default 24, range 1-168). Optional `lookback_days` (default 7, max 30).

### `reconcile_dashboard`

Runs all completion-candidate detection rules and returns a full automation-health snapshot including stale tasks, candidates, proposal backlog, and missing-handoff status. Does NOT mutate tasks.

No args. Note: it still writes `completion_candidates` rows via the same `DetectAndUpsert` call used by `detect_completion_candidates` — "does not mutate tasks" refers only to the GTD `tasks` table.

---

### `record_outcome`

Record the result of an executed task, decision, sprint, or project. Closes the Action→Result loop by capturing what actually happened. Optionally link to behavior rules via `related_rule_ids` so the behavior governance scheduler can update their confidence scores.

| Arg | Required |
|-----|----------|
| `entity_type` (`task`/`decision`/`sprint`/`project`) `entity_id` (UUID) `result` (`success`/`failure`/`partial`/`unknown`/`regressed`) | Yes |
| `notes` (max 500 runes) `metrics_json` `related_rule_ids` (JSON array of UUIDs, max 20) | No |

### `evaluate_outcome`

Attach structured analysis to an existing outcome. Captures root-cause analysis, lessons learned, and improvement suggestions to feed the reflection loop.

| Arg | Required |
|-----|----------|
| `outcome_id` `analysis` (max 500 runes) | Yes |
| `lessons_json` `suggestions_json` (JSON arrays) | No |

### `list_recent_outcomes`

List recent outcomes ordered by created_at DESC. Optionally filter by entity_type. Use to review recent execution results.

Optional `entity_type`. Optional `limit` (default 20, max 100).

### `find_failed_patterns`

Retrieve recent failure and regression outcomes together with their evaluations. Use to identify recurring failure patterns and improvement opportunities.

Optional `limit` (default 10, max 100).

---

### `extract_skill`

Extracts and persists a reusable skill definition from the current session. Provide a name, description, trigger conditions, step-by-step approach, common failure modes, and a verification checklist.

| Arg | Required |
|-----|----------|
| `name` (max 200 chars) | Yes |
| `description` (max 5000 chars) `triggers` `steps` `failure_modes` `verification_checklist` `source_atom_ids` (all comma-separated) | No |

### `search_skills`

Searches persisted skills by name or description. Returns matching skills ordered by success_count DESC.

`query` required. Optional `limit` (default 10).

### `use_skill`

Records that a skill was applied successfully. Increments success_count and updates last_used_at. Returns the updated skill.

`skill_id` required.

### `update_skill_from_outcome`

Records a success or failure outcome for a skill execution. Appends the outcome reference and notes to the skill's examples log and increments the appropriate counter.

`skill_id` required. Optional `outcome_id`, `success` (boolean), `notes` (max 2000 chars).

### `list_relevant_skills`

Lists skills most relevant to the current task context. Skills are ordered by success_count DESC, last_used_at DESC. Optionally filter by keyword query.

Optional `query`. Optional `limit` (default 10).

---

### `generate_project_status`

Returns a Haiku-generated status snapshot for the given project slug (sprint_summary, gap_analysis, sota_catchup_pct, pending_summary). Cached for 24 h; use `force_refresh=true` to regenerate immediately. CALL `generate_project_status` instead of re-reading 100+ decisions manually.

`slug` required (must match `^[a-zA-Z0-9_-]+$`, ≤ 64 chars — same allow-list as `upsert_project_arch`, guards against prompt-boundary injection into the Haiku snapshot prompt). Optional `force_refresh` (boolean).

---

### `promote_atom_to_knowledge`

Manually promote a consolidated memory atom into a pending knowledge proposal. The atom must have content >= 80 chars and at least 2 tags. The proposal is created with `proposed_by='atom_bridge'` and requires user confirmation via `confirm_proposal` before becoming a real knowledge item. Deduplication happens at confirm time (similarity threshold 0.88).

`atom_id` required.

### `traverse_atoms`

Traverse the memory atom graph from a starting atom, following links up to depth hops. Returns visited atoms and links. Use to explore how facts relate.

`start_atom_id` required. Optional `depth` (1-5, default 2).

### `search_atoms`

Search memory atoms by keyword across content, keywords, and tags. Returns atoms whose content or metadata contains the query string.

`query` required. Optional `limit` (default 10, max 20).

---

### `reconcile_merged_prs`

Accepts a Claude-supplied list of recently-merged PRs and matches them against pending/in_progress tasks by `pr_url` or `branch_name`. TWO-STEP: the first call (`confirm` absent/false) only PREVIEWS matches — returns `{matches, ambiguous, no_match, reconcile_token, expires_at}` — and does NOT complete any tasks. The second call MUST include `confirm=true` and the `reconcile_token` from the first call to actually apply the completions computed during preview; on the second call any resent `payload` is ignored entirely — only the exact match-set computed during preview is applied. Tokens expire after 60s. Idempotent: a preview for an already-applied batch shows those tasks in `no_match`. Ambiguous branch_name matches (>1 task on same branch) auto-apply the most-recent task; siblings are surfaced ONLY in this call's own `ambiguous` response field for manual resolution — they are NOT persisted as completion_candidates rows, so a client that needs to act on them later must capture this response.

| Arg | Required |
|-----|----------|
| `payload` | Required on the preview call (`confirm` absent/false); ignored entirely when `confirm=true`. JSON string of `{"merged_prs":[{"url":"...","head_ref":"...","merged_at":"RFC3339","title":"...","body":"...","repo":"owner/repo"}, ...]}`. Same shape as `POST /api/tasks/reconcile-merged-prs`. |
| `confirm` `reconcile_token` | No — required together on the second (confirming) call |

---

### `list_playbooks`

Returns procedural playbooks — generalized rules derived from past decisions. Call BEFORE responding to any complex task to check if a matching rule exists. Example: before architecting a new feature, call `list_playbooks` with `context_keywords` to see if there is a relevant past pattern to follow.

Optional `context_keywords` (space- or comma-separated).

---

### `add_procedural`

Saves a reusable how-to memory: title, when to use it, markdown approach, tools used, and files typically touched. Call after completing a complex task to capture the approach for future reuse.

| Arg | Required |
|-----|----------|
| `title` (max 200 chars) `when_to_use` (max 2000 chars) | Yes |
| `approach_md` (max 20000 chars) `repo_name` `tools_used` `files_touched` (comma-separated) | No |

### `query_procedural`

Returns procedural memories matching keywords. Searches title, when_to_use, and approach_md. Results ordered by success_count DESC so the most-proven approaches surface first.

`keywords` required. Optional `repo_name`, `limit` (default 10, max 20).

### `mark_procedural_used`

Increments the success_count of a procedural memory and sets last_used_at. Call after successfully applying a procedural memory to reinforce its ranking.

`id` required.

### `recall`

Unified cross-type memory search. Searches episodic (recent session handoffs), semantic (knowledge + decisions), and procedural memories simultaneously. Use for broad 'what do I know about X' queries.

`query` required. Optional `types` (comma-separated: episodic, semantic, procedural; default all three).

---

## Automatic trigger mechanisms

**Classify middleware:** After each significant tool call, a background goroutine may auto-log implicit decisions or follow-up tasks (AI-powered, rate-limited to 60 calls/rolling window).

**Stop hook:** `scripts/wbt-stop-hook.sh` calls `POST /api/auto-handoff` and `POST /api/activity` when Claude Code exits.

**Session start:** `UserPromptSubmit` hook calls `get_today_context` automatically at the start of each conversation.
