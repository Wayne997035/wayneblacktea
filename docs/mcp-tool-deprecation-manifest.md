# MCP Tool Deprecation Manifest

**Status:** H1 read-only inventory (SA-2026-07-21). No tool registration, schema, or
description was modified to produce this document — see `git diff --stat` on this PR,
which touches only this file.

**As-of:** `e8b52a1` (`feat: wbt-2.0 P6 evaluation harness + GTD assignee enforcement`),
2026-07-23.

**Scope:** Every MCP tool registered by `internal/mcp/server.go`'s `MCPServer()` — i.e.
every tool a Claude Code / Discord / CLI client sees over stdio or the `/mcp` HTTP
transport. This document freezes an exact inventory + classification; it does not
delete, deprecate, or change any tool.

---

## 0. Methodology

1. **Inventory** — `grep -n "mcp.NewTool(" internal/mcp/*.go` (excluding `_test.go`)
   extracts every literal tool-name string. Cross-checked two independent ways:
   - `TestMCPServer_AllRegisteredToolsClassified` (`internal/mcp/tools_health_discipline_test.go:360`)
     builds a real `*Server` against an in-memory SQLite backend, calls
     `srv.MCPServer().ListTools()`, and asserts every returned name is classified in
     `internal/discipline/discipline.go`'s `MutatingTools` ∪ `DeliberatelyExcludedTools`
     maps — **PASS** (ran read-only, no files changed).
   - A throwaway `go test` (`internal/mcp/zzz_manifest_count_test.go`, written and deleted
     during this analysis — not part of this commit) printed
     `ms.ListTools()` length directly: **91**.
   - Static grep count and `docs/mcp-tools.md`'s existing permissions-matrix table (91
     rows) both independently agree: **91 registered tools**, zero drift between static
     source, runtime registration, and existing docs.
   - **Updated for progressive disclosure:** the count is now **92** — `expand_tools`
     (`internal/mcp/tools_expand.go`) was added. The inventory below carries a
     **Tool-list tier** column: `core` = advertised in `tools/list` unconditionally
     (`internal/mcp/toolgroups.go` `coreToolNames`), `expandable (<group>)` = revealed
     on demand by `expand_tools`. **Tier is a catalogue-visibility fact, NOT a usage
     signal** — mcp-go applies tool filters only in `handleListTools`, so an
     `expandable` tool is exactly as callable as a `core` one and its
     USED/LATENT classification is unaffected.

2. **Consumer classification.** Because MCP tools are invoked over the protocol boundary
   by external LLM clients (Claude Code, Discord bot, CLI), no Go code calls a tool
   handler function by name in the traditional "caller" sense. The four real,
   grep/read-verified in-repo signals of active operational wiring are:
   - **S1 — the server-side usage protocol, `mcpInstructions` ∪ `mcpProtocolFull`.**
     `mcpInstructions` (`internal/mcp/server.go`) is the budgeted subset injected at
     capability-negotiation time (`server.WithInstructions(mcpInstructions)`);
     `mcpProtocolFull` (`internal/mcp/tools_onboarding.go`) is
     `mcpInstructions + mcpProtocolAppendix`, returned by the `initial_instructions`
     tool. **Both halves count as S1.** *(Revised when progressive disclosure landed:
     the protocol string was compressed from 4 689 to 1 989 runes and the routing rows
     for `list_playbooks`, `add_procedural`, `query_procedural`,
     `mark_procedural_used` and `recall` moved into the appendix. Those tools did NOT
     lose their operational wiring — they are still routed by the same protocol text,
     just served on demand. Reading only the injected half would misclassify five
     actively-routed tools as LATENT and invite a proposal to delete live tools.)*
     Tool names appearing in that routing table are the strongest signal available
     short of production telemetry.
   - **S2 — middleware tool-name switches**: `autoLogEntry` (`middleware_autolog.go:150-201`)
     and `significantTools` (`middleware_classify.go:27-33`) are Go `switch`/`map`
     statements that literally match on the tool-name string to trigger extra
     server-side behavior (activity-log write, AI classification). Membership is
     direct code evidence the tool's calls are treated as significant events.
   - **S3 — `docs/mcp-tools.md` explicit "Call ..." trigger.** All 91 tools have a
     doc entry (param reference), but only a subset carry an imperative "Call X when
     Y" sentence — grep-verified (`grep -A1 '### ' | grep -E '\bCall\b'`). This is the
     maintainer's own operational guidance to the LLM client, distinct from generic
     parameter documentation.
   - **S4 — automated production trigger outside the MCP protocol**: a
     `internal/scheduler/*.go` cron job or `.github/workflows/*.yml` CI job that calls
     the *same underlying domain-store method* the tool wraps. Verified per-tool below.
   - **S5 — local dev telemetry** (best-effort only, see §"Telemetry" below).

   `USED` = at least one of S1–S4. `LATENT` = documented (100% of tools are, in
   `docs/mcp-tools.md`) but zero S1–S4 signal — retained per dispatch boundary, since
   "no in-repo trigger found" is not proof of "never called by a real LLM client."
   `UNREACHABLE(P4)` = LATENT **and** the maintainer's own phase-planning doc
   (`docs/internal/wayneblacktea-2.0-development-prompt.md`) marks the owning domain
   as a not-yet-shipped phase, confirmed by `git log --oneline --all | grep -iE
   "P3|P4|P5|skill.compiler"` returning **zero** commits. `DEAD-CANDIDATE` = LATENT
   **and** code-proven functional redundancy with another registered, USED/LATENT tool
   (identical store call + identical response construction, not just "similar").

3. **Telemetry.** `internal/discipline` persists every mutating tool call to
   `discipline_events` (30-day TTL, `build/Taskfile.yml discipline-prune`). This
   analysis is read-only and does **not** connect to the production Aiven Postgres
   instance (Boundaries: no prod DB). A local dev SQLite file
   (`/Users/waynechen/_project/wayneblacktea/wayneblacktea.db`, outside this worktree,
   pre-existing) was queried read-only (`sqlite3 -readonly ... "SELECT tool_name,
   COUNT(*), MIN(observed_at), MAX(observed_at) FROM discipline_events GROUP BY
   tool_name"`) as a best-effort signal only. Result: **3 rows total** — `add_task`
   (1, 2026-07-20T08:14:54Z), `begin_task` (1, 2026-07-20T08:15:00Z), `update_task`
   (1, 2026-07-20T08:33:31Z). This is local manual-testing noise, not the production
   30-day retained history the SA spec refers to as "full history." Every other tool
   shows 0 events in this local DB — **this is explicitly NOT removal authority**
   (Threat section, below): unknown external MCP clients and the untested/failed-call
   population remain invisible to this file.

---

## 1. Full Inventory (92 tools)

| Tool | Tool-list tier | Consumers (evidence) | Classification | Replacement | Deprecation-eligible | Telemetry |
|---|---|---|---|---|---|---|
| `add_knowledge` | expandable (knowledge) | docs/mcp-tools.md explicit 'Call' trigger | USED | - | No -- actively wired (USED) | 0 events in local dev sqlite discipline_events; production (Aiven Postgres, 30-day retention) telemetry unavailable -- not queried (Boundaries: no prod DB connection) |
| `add_procedural` | expandable (procedural) | mcpProtocolFull routing table (internal/mcp/tools_onboarding.go appendix; moved out of the injected half by the progressive-disclosure compression, still S1) / docs/mcp-tools.md explicit 'Call' trigger | USED | - | No -- actively wired (USED) | 0 events in local dev sqlite discipline_events; production (Aiven Postgres, 30-day retention) telemetry unavailable -- not queried (Boundaries: no prod DB connection) |
| `add_task` | core | mcpInstructions routing table (internal/mcp/server.go) / middleware(autoLogEntry, internal/mcp/middleware_autolog.go/middleware_classify.go) / docs/mcp-tools.md explicit 'Call' trigger | USED | - | No -- actively wired (USED) | 1 event (local dev sqlite discipline_events, observed_at=2026-07-20T08:14:54.716Z) -- NOT representative of prod 30-day retention window |
| `add_vision_item` | core | mcpInstructions routing table (internal/mcp/server.go) / docs/mcp-tools.md explicit 'Call' trigger | USED | - | No -- actively wired (USED) | 0 events in local dev sqlite discipline_events; production (Aiven Postgres, 30-day retention) telemetry unavailable -- not queried (Boundaries: no prod DB connection) |
| `analyze_agent_behavior` | expandable (watchdog) | docs/mcp-tools.md (permissions matrix + param reference; no call-trigger sentence, no middleware/scheduler wiring found) | LATENT | - | No -- retained (LATENT, documented + registered; zero code-trigger is not removal authority) | 0 events in local dev sqlite discipline_events; production (Aiven Postgres, 30-day retention) telemetry unavailable -- not queried (Boundaries: no prod DB connection) |
| `analyze_recent_patterns` | expandable (reflection) | docs/mcp-tools.md (permissions matrix + param reference; no call-trigger sentence, no middleware/scheduler wiring found) | LATENT | - | No -- retained (LATENT, documented + registered; zero code-trigger is not removal authority) | 0 events in local dev sqlite discipline_events; production (Aiven Postgres, 30-day retention) telemetry unavailable -- not queried (Boundaries: no prod DB connection) |
| `apply_behavior_rules` | expandable (behavior_rule) | internal/scheduler/behavior_governance.go runBehaviorGovernance (weekly job) calls behaviorrule.Store.ApplyOutcome | USED | - | No -- actively wired (USED) | 0 events in local dev sqlite discipline_events; production (Aiven Postgres, 30-day retention) telemetry unavailable -- not queried (Boundaries: no prod DB connection) |
| `assemble_context` | expandable (context_pack) | docs/mcp-tools.md explicit 'Call' trigger | USED | - | No -- actively wired (USED) | 0 events in local dev sqlite discipline_events; production (Aiven Postgres, 30-day retention) telemetry unavailable -- not queried (Boundaries: no prod DB connection) |
| `begin_task` | expandable (gtd) | middleware(autoLogEntry, internal/mcp/middleware_autolog.go/middleware_classify.go) / docs/mcp-tools.md explicit 'Call' trigger | USED | - | No -- actively wired (USED) | 1 event (local dev sqlite discipline_events, observed_at=2026-07-20T08:15:00.527Z) -- NOT representative of prod 30-day retention window |
| `checkpoint_work` | expandable (work_session) | middleware(autoLogEntry, internal/mcp/middleware_autolog.go/middleware_classify.go) | USED | - | No -- actively wired (USED) | 0 events in local dev sqlite discipline_events; production (Aiven Postgres, 30-day retention) telemetry unavailable -- not queried (Boundaries: no prod DB connection) |
| `closeout_session_check` | expandable (closeout) | docs/mcp-tools.md explicit 'Call' trigger | USED | - | No -- actively wired (USED) | 0 events in local dev sqlite discipline_events; production (Aiven Postgres, 30-day retention) telemetry unavailable -- not queried (Boundaries: no prod DB connection) |
| `complete_task` | core | mcpInstructions routing table (internal/mcp/server.go) / middleware(autoLogEntry+significantTools, internal/mcp/middleware_autolog.go/middleware_classify.go) / docs/mcp-tools.md explicit 'Call' trigger / NOTE: Auto-seeds a draft outcome and has no reopen path -- set_task_status is NOT redundant with this tool. | USED | - | No -- actively wired (USED) | 0 events in local dev sqlite discipline_events; production (Aiven Postgres, 30-day retention) telemetry unavailable -- not queried (Boundaries: no prod DB connection) |
| `confirm_plan` | core | mcpInstructions routing table (internal/mcp/server.go) / middleware(autoLogEntry, internal/mcp/middleware_autolog.go/middleware_classify.go) / docs/mcp-tools.md explicit 'Call' trigger | USED | - | No -- actively wired (USED) | 0 events in local dev sqlite discipline_events; production (Aiven Postgres, 30-day retention) telemetry unavailable -- not queried (Boundaries: no prod DB connection) |
| `confirm_proposal` | expandable (proposal) | middleware(significantTools, internal/mcp/middleware_autolog.go/middleware_classify.go) / docs/mcp-tools.md explicit 'Call' trigger / NOTE: Singular accept/reject. Batch sibling confirm_proposals exists but reject paths diverge (confirm_proposal.reject -> proposal.Resolve with distinct 'not found or already resolved' error; confirm_proposals.reject -> proposal.BatchConfirm, different response shape/error semantics -- tools_proposal.go:206-330) and confirm_proposals is NOT in the classify-middleware significantTools set (docs/mcp-tools.md:453) so auto-logging coverage differs. Consolidation possible but blocked on singular/batch parity -- not eligible today. | USED | - | No -- actively wired (USED) | 0 events in local dev sqlite discipline_events; production (Aiven Postgres, 30-day retention) telemetry unavailable -- not queried (Boundaries: no prod DB connection) |
| `confirm_proposals` | expandable (proposal) | docs/mcp-tools.md (permissions matrix + param reference; no call-trigger sentence, no middleware/scheduler wiring found) / NOTE: Batch sibling of confirm_proposal; accept path reuses acceptProposal per-id (parity), reject path uses a different store method (BatchConfirm) with different response shape -- see confirm_proposal note. | LATENT | - | No -- retained (LATENT, documented + registered; zero code-trigger is not removal authority) | 0 events in local dev sqlite discipline_events; production (Aiven Postgres, 30-day retention) telemetry unavailable -- not queried (Boundaries: no prod DB connection) |
| `create_concept` | expandable (learning) | docs/mcp-tools.md (permissions matrix + param reference; no call-trigger sentence, no middleware/scheduler wiring found) | LATENT | - | No -- retained (LATENT, documented + registered; zero code-trigger is not removal authority) | 0 events in local dev sqlite discipline_events; production (Aiven Postgres, 30-day retention) telemetry unavailable -- not queried (Boundaries: no prod DB connection) |
| `create_goal` | expandable (gtd) | docs/mcp-tools.md (permissions matrix + param reference; no call-trigger sentence, no middleware/scheduler wiring found) | LATENT | - | No -- retained (LATENT, documented + registered; zero code-trigger is not removal authority) | 0 events in local dev sqlite discipline_events; production (Aiven Postgres, 30-day retention) telemetry unavailable -- not queried (Boundaries: no prod DB connection) |
| `create_project` | expandable (gtd) | docs/mcp-tools.md (permissions matrix + param reference; no call-trigger sentence, no middleware/scheduler wiring found) | LATENT | - | No -- retained (LATENT, documented + registered; zero code-trigger is not removal authority) | 0 events in local dev sqlite discipline_events; production (Aiven Postgres, 30-day retention) telemetry unavailable -- not queried (Boundaries: no prod DB connection) |
| `delete_task` | expandable (gtd) | docs/mcp-tools.md (permissions matrix + param reference; no call-trigger sentence, no middleware/scheduler wiring found) | LATENT | - | No -- retained (LATENT, documented + registered; zero code-trigger is not removal authority) | 0 events in local dev sqlite discipline_events; production (Aiven Postgres, 30-day retention) telemetry unavailable -- not queried (Boundaries: no prod DB connection) |
| `deprecate_behavior_rule` | expandable (behavior_rule) | internal/scheduler/behavior_governance.go autoDeprecateStaleLowConfidenceRules (weekly job) calls behaviorrule.Store.Deprecate | USED | - | No -- actively wired (USED) | 0 events in local dev sqlite discipline_events; production (Aiven Postgres, 30-day retention) telemetry unavailable -- not queried (Boundaries: no prod DB connection) |
| `detect_completion_candidates` | expandable (dashboard) | docs/mcp-tools.md (permissions matrix + param reference; no call-trigger sentence, no middleware/scheduler wiring found) | LATENT | - | No -- retained (LATENT, documented + registered; zero code-trigger is not removal authority) | 0 events in local dev sqlite discipline_events; production (Aiven Postgres, 30-day retention) telemetry unavailable -- not queried (Boundaries: no prod DB connection) |
| `detect_unclosed_loops` | expandable (watchdog) | docs/mcp-tools.md (permissions matrix + param reference; no call-trigger sentence, no middleware/scheduler wiring found) | LATENT | - | No -- retained (LATENT, documented + registered; zero code-trigger is not removal authority) | 0 events in local dev sqlite discipline_events; production (Aiven Postgres, 30-day retention) telemetry unavailable -- not queried (Boundaries: no prod DB connection) |
| `evaluate_outcome` | core | docs/mcp-tools.md (permissions matrix + param reference; no call-trigger sentence, no middleware/scheduler wiring found) | LATENT | - | No -- retained (LATENT, documented + registered; zero code-trigger is not removal authority) | 0 events in local dev sqlite discipline_events; production (Aiven Postgres, 30-day retention) telemetry unavailable -- not queried (Boundaries: no prod DB connection) |
| `expand_tools` | core | mcpInstructions routing (internal/mcp/server.go) / mcpProtocolFull per-tool detail (internal/mcp/tools_onboarding.go) / docs/mcp-tools.md explicit 'Call' trigger | USED | - | No -- actively wired (USED); it is the entry point that makes the other 74 tools discoverable | Added 2026-08-08 with progressive disclosure; no telemetry history yet |
| `extract_skill` | expandable (skill) | docs/internal/wayneblacktea-2.0-development-prompt.md Phase 4 'Skill Compiler' (no compiler/automation wired -- git log --all has zero P4/skill-compiler commits); docs/mcp-tools.md documents params only, no call-trigger | UNREACHABLE(P4) | - | No -- retained (UNREACHABLE P4 learning tool, explicitly preserved per dispatch boundary) | 0 events in local dev sqlite discipline_events; production (Aiven Postgres, 30-day retention) telemetry unavailable -- not queried (Boundaries: no prod DB connection) |
| `find_failed_patterns` | expandable (outcome) | docs/mcp-tools.md (permissions matrix + param reference; no call-trigger sentence, no middleware/scheduler wiring found) | LATENT | - | No -- retained (LATENT, documented + registered; zero code-trigger is not removal authority) | 0 events in local dev sqlite discipline_events; production (Aiven Postgres, 30-day retention) telemetry unavailable -- not queried (Boundaries: no prod DB connection) |
| `finish_work` | expandable (work_session) | middleware(autoLogEntry, internal/mcp/middleware_autolog.go/middleware_classify.go) | USED | - | No -- actively wired (USED) | 0 events in local dev sqlite discipline_events; production (Aiven Postgres, 30-day retention) telemetry unavailable -- not queried (Boundaries: no prod DB connection) |
| `generate_project_status` | expandable (status) | docs/mcp-tools.md explicit 'Call' trigger | USED | - | No -- actively wired (USED) | 0 events in local dev sqlite discipline_events; production (Aiven Postgres, 30-day retention) telemetry unavailable -- not queried (Boundaries: no prod DB connection) |
| `generate_reflection` | expandable (reflection) | docs/mcp-tools.md (permissions matrix + param reference; no call-trigger sentence, no middleware/scheduler wiring found) | LATENT | - | No -- retained (LATENT, documented + registered; zero code-trigger is not removal authority) | 0 events in local dev sqlite discipline_events; production (Aiven Postgres, 30-day retention) telemetry unavailable -- not queried (Boundaries: no prod DB connection) |
| `get_active_work` | expandable (work_session) | docs/mcp-tools.md (permissions matrix + param reference; no call-trigger sentence, no middleware/scheduler wiring found) | LATENT | - | No -- retained (LATENT, documented + registered; zero code-trigger is not removal authority) | 0 events in local dev sqlite discipline_events; production (Aiven Postgres, 30-day retention) telemetry unavailable -- not queried (Boundaries: no prod DB connection) |
| `get_due_reviews` | expandable (learning) | internal/scheduler/scheduler.go sendDailyReviewReminder (Discord daily reminder) + sendDailyNotionBriefing (DueReviews count in Notion briefing) call learning.CountDueReviews | USED | - | No -- actively wired (USED) | 0 events in local dev sqlite discipline_events; production (Aiven Postgres, 30-day retention) telemetry unavailable -- not queried (Boundaries: no prod DB connection) |
| `get_latest_reflection` | expandable (reflection) | docs/mcp-tools.md (permissions matrix + param reference; no call-trigger sentence, no middleware/scheduler wiring found) | LATENT | - | No -- retained (LATENT, documented + registered; zero code-trigger is not removal authority) | 0 events in local dev sqlite discipline_events; production (Aiven Postgres, 30-day retention) telemetry unavailable -- not queried (Boundaries: no prod DB connection) |
| `get_project` | expandable (gtd) | docs/mcp-tools.md (permissions matrix + param reference; no call-trigger sentence, no middleware/scheduler wiring found) | LATENT | - | No -- retained (LATENT, documented + registered; zero code-trigger is not removal authority) | 0 events in local dev sqlite discipline_events; production (Aiven Postgres, 30-day retention) telemetry unavailable -- not queried (Boundaries: no prod DB connection) |
| `get_project_arch` | core | mcpInstructions routing table (internal/mcp/server.go) | USED | - | No -- actively wired (USED) | 0 events in local dev sqlite discipline_events; production (Aiven Postgres, 30-day retention) telemetry unavailable -- not queried (Boundaries: no prod DB connection) |
| `get_task` | expandable (gtd) | docs/mcp-tools.md (permissions matrix + param reference; no call-trigger sentence, no middleware/scheduler wiring found) | LATENT | - | No -- retained (LATENT, documented + registered; zero code-trigger is not removal authority) | 0 events in local dev sqlite discipline_events; production (Aiven Postgres, 30-day retention) telemetry unavailable -- not queried (Boundaries: no prod DB connection) |
| `get_today_context` | core | mcpInstructions routing table (internal/mcp/server.go) / docs/mcp-tools.md explicit 'Call' trigger | USED | - | No -- actively wired (USED) | 0 events in local dev sqlite discipline_events; production (Aiven Postgres, 30-day retention) telemetry unavailable -- not queried (Boundaries: no prod DB connection) |
| `get_upcoming_work` | expandable (gtd) | docs/mcp-tools.md (permissions matrix + param reference; no call-trigger sentence, no middleware/scheduler wiring found) | LATENT | - | No -- retained (LATENT, documented + registered; zero code-trigger is not removal authority) | 0 events in local dev sqlite discipline_events; production (Aiven Postgres, 30-day retention) telemetry unavailable -- not queried (Boundaries: no prod DB connection) |
| `get_work_session_trace` | expandable (work_session) | docs/mcp-tools.md (permissions matrix + param reference; no call-trigger sentence, no middleware/scheduler wiring found) | LATENT | - | No -- retained (LATENT, documented + registered; zero code-trigger is not removal authority) | 0 events in local dev sqlite discipline_events; production (Aiven Postgres, 30-day retention) telemetry unavailable -- not queried (Boundaries: no prod DB connection) |
| `initial_instructions` | core | docs/mcp-tools.md explicit 'Call' trigger | USED | - | No -- actively wired (USED) | 0 events in local dev sqlite discipline_events; production (Aiven Postgres, 30-day retention) telemetry unavailable -- not queried (Boundaries: no prod DB connection) |
| `list_active_repos` | expandable (context) | docs/mcp-tools.md (permissions matrix + param reference; no call-trigger sentence, no middleware/scheduler wiring found) | LATENT | - | No -- retained (LATENT, documented + registered; zero code-trigger is not removal authority) | 0 events in local dev sqlite discipline_events; production (Aiven Postgres, 30-day retention) telemetry unavailable -- not queried (Boundaries: no prod DB connection) |
| `list_behavior_rules` | expandable (behavior_rule) | docs/mcp-tools.md (permissions matrix + param reference; no call-trigger sentence, no middleware/scheduler wiring found) | LATENT | - | No -- retained (LATENT, documented + registered; zero code-trigger is not removal authority) | 0 events in local dev sqlite discipline_events; production (Aiven Postgres, 30-day retention) telemetry unavailable -- not queried (Boundaries: no prod DB connection) |
| `list_decisions` | core | docs/mcp-tools.md explicit 'Call' trigger | USED | - | No -- actively wired (USED) | 0 events in local dev sqlite discipline_events; production (Aiven Postgres, 30-day retention) telemetry unavailable -- not queried (Boundaries: no prod DB connection) |
| `list_goals` | expandable (gtd) | docs/mcp-tools.md (permissions matrix + param reference; no call-trigger sentence, no middleware/scheduler wiring found) | LATENT | - | No -- retained (LATENT, documented + registered; zero code-trigger is not removal authority) | 0 events in local dev sqlite discipline_events; production (Aiven Postgres, 30-day retention) telemetry unavailable -- not queried (Boundaries: no prod DB connection) |
| `list_knowledge` | expandable (knowledge) | docs/mcp-tools.md (permissions matrix + param reference; no call-trigger sentence, no middleware/scheduler wiring found) | LATENT | - | No -- retained (LATENT, documented + registered; zero code-trigger is not removal authority) | 0 events in local dev sqlite discipline_events; production (Aiven Postgres, 30-day retention) telemetry unavailable -- not queried (Boundaries: no prod DB connection) |
| `list_pending_proposals` | expandable (proposal) | docs/mcp-tools.md (permissions matrix + param reference; no call-trigger sentence, no middleware/scheduler wiring found) | LATENT | - | No -- retained (LATENT, documented + registered; zero code-trigger is not removal authority) | 0 events in local dev sqlite discipline_events; production (Aiven Postgres, 30-day retention) telemetry unavailable -- not queried (Boundaries: no prod DB connection) |
| `list_playbooks` | expandable (playbook) | mcpProtocolFull routing table (internal/mcp/tools_onboarding.go appendix; moved out of the injected half by the progressive-disclosure compression, still S1) / docs/mcp-tools.md explicit 'Call' trigger | USED | - | No -- actively wired (USED) | 0 events in local dev sqlite discipline_events; production (Aiven Postgres, 30-day retention) telemetry unavailable -- not queried (Boundaries: no prod DB connection) |
| `list_projects` | expandable (gtd) | docs/mcp-tools.md (permissions matrix + param reference; no call-trigger sentence, no middleware/scheduler wiring found) | LATENT | - | No -- retained (LATENT, documented + registered; zero code-trigger is not removal authority) | 0 events in local dev sqlite discipline_events; production (Aiven Postgres, 30-day retention) telemetry unavailable -- not queried (Boundaries: no prod DB connection) |
| `list_recent_outcomes` | expandable (outcome) | docs/mcp-tools.md (permissions matrix + param reference; no call-trigger sentence, no middleware/scheduler wiring found) | LATENT | - | No -- retained (LATENT, documented + registered; zero code-trigger is not removal authority) | 0 events in local dev sqlite discipline_events; production (Aiven Postgres, 30-day retention) telemetry unavailable -- not queried (Boundaries: no prod DB connection) |
| `list_recent_work_sessions` | expandable (work_session) | docs/mcp-tools.md (permissions matrix + param reference; no call-trigger sentence, no middleware/scheduler wiring found) | LATENT | - | No -- retained (LATENT, documented + registered; zero code-trigger is not removal authority) | 0 events in local dev sqlite discipline_events; production (Aiven Postgres, 30-day retention) telemetry unavailable -- not queried (Boundaries: no prod DB connection) |
| `list_reflections` | expandable (reflection) | docs/mcp-tools.md (permissions matrix + param reference; no call-trigger sentence, no middleware/scheduler wiring found) | LATENT | - | No -- retained (LATENT, documented + registered; zero code-trigger is not removal authority) | 0 events in local dev sqlite discipline_events; production (Aiven Postgres, 30-day retention) telemetry unavailable -- not queried (Boundaries: no prod DB connection) |
| `list_relevant_skills` | expandable (skill) | docs/internal/wayneblacktea-2.0-development-prompt.md Phase 4 'Skill Compiler' (no compiler/automation wired -- git log --all has zero P4/skill-compiler commits); docs/mcp-tools.md documents params only, no call-trigger | UNREACHABLE(P4) | - | No -- retained (UNREACHABLE P4 learning tool, explicitly preserved per dispatch boundary) | 0 events in local dev sqlite discipline_events; production (Aiven Postgres, 30-day retention) telemetry unavailable -- not queried (Boundaries: no prod DB connection) |
| `list_tasks` | core | docs/mcp-tools.md (permissions matrix + param reference; no call-trigger sentence, no middleware/scheduler wiring found) | LATENT | - | No -- retained (LATENT, documented + registered; zero code-trigger is not removal authority) | 0 events in local dev sqlite discipline_events; production (Aiven Postgres, 30-day retention) telemetry unavailable -- not queried (Boundaries: no prod DB connection) |
| `list_vision_items` | expandable (vision) | docs/mcp-tools.md (permissions matrix + param reference; no call-trigger sentence, no middleware/scheduler wiring found) | LATENT | - | No -- retained (LATENT, documented + registered; zero code-trigger is not removal authority) | 0 events in local dev sqlite discipline_events; production (Aiven Postgres, 30-day retention) telemetry unavailable -- not queried (Boundaries: no prod DB connection) |
| `log_activity` | expandable (gtd) | docs/mcp-tools.md (permissions matrix + param reference; no call-trigger sentence, no middleware/scheduler wiring found) | LATENT | - | No -- retained (LATENT, documented + registered; zero code-trigger is not removal authority) | 0 events in local dev sqlite discipline_events; production (Aiven Postgres, 30-day retention) telemetry unavailable -- not queried (Boundaries: no prod DB connection) |
| `log_decision` | core | mcpInstructions routing table (internal/mcp/server.go) / middleware(autoLogEntry, internal/mcp/middleware_autolog.go/middleware_classify.go) / docs/mcp-tools.md explicit 'Call' trigger | USED | - | No -- actively wired (USED) | 0 events in local dev sqlite discipline_events; production (Aiven Postgres, 30-day retention) telemetry unavailable -- not queried (Boundaries: no prod DB connection) |
| `mark_loop_resolved` | expandable (watchdog) | docs/mcp-tools.md explicit 'Call' trigger | USED | - | No -- actively wired (USED) | 0 events in local dev sqlite discipline_events; production (Aiven Postgres, 30-day retention) telemetry unavailable -- not queried (Boundaries: no prod DB connection) |
| `mark_next_action_done` | expandable (session) | docs/mcp-tools.md (permissions matrix + param reference; no call-trigger sentence, no middleware/scheduler wiring found) / NOTE: NOT equivalent to set_session_handoff (code-verified): mutates a single next_actions[step].status on an existing handoff (tools_session.go:101-122) without touching intent/context_summary/other steps; set_session_handoff always replaces the whole handoff record and has no per-step selector. | LATENT | - | No -- retained (LATENT, documented + registered; zero code-trigger is not removal authority) | 0 events in local dev sqlite discipline_events; production (Aiven Postgres, 30-day retention) telemetry unavailable -- not queried (Boundaries: no prod DB connection) |
| `mark_procedural_used` | expandable (procedural) | mcpProtocolFull routing table (internal/mcp/tools_onboarding.go appendix; moved out of the injected half by the progressive-disclosure compression, still S1) / docs/mcp-tools.md explicit 'Call' trigger | USED | - | No -- actively wired (USED) | 0 events in local dev sqlite discipline_events; production (Aiven Postgres, 30-day retention) telemetry unavailable -- not queried (Boundaries: no prod DB connection) |
| `navigate_knowledge` | expandable (knowledge_nav) | docs/mcp-tools.md (permissions matrix + param reference; no call-trigger sentence, no middleware/scheduler wiring found) | LATENT | - | No -- retained (LATENT, documented + registered; zero code-trigger is not removal authority) | 0 events in local dev sqlite discipline_events; production (Aiven Postgres, 30-day retention) telemetry unavailable -- not queried (Boundaries: no prod DB connection) |
| `outline_knowledge` | expandable (knowledge_nav) | code-identical: handleOutlineKnowledge and the non-root branch of handleNavigateKnowledge both call s.knowledge.ListChildren(ctx,id) + navItemFromDB and return jsonText(nav) verbatim (internal/mcp/tools_knowledge_nav.go:65-114) | DEAD-CANDIDATE | navigate_knowledge(parent_id=item_id) | Tentative -- code-only evidence (see consumers), needs telemetry confirm before H4 removal | 0 events in local dev sqlite discipline_events; production (Aiven Postgres, 30-day retention) telemetry unavailable -- not queried (Boundaries: no prod DB connection) |
| `promote_atom_to_knowledge` | expandable (atom) | docs/mcp-tools.md (permissions matrix + param reference; no call-trigger sentence, no middleware/scheduler wiring found) | LATENT | - | No -- retained (LATENT, documented + registered; zero code-trigger is not removal authority) | 0 events in local dev sqlite discipline_events; production (Aiven Postgres, 30-day retention) telemetry unavailable -- not queried (Boundaries: no prod DB connection) |
| `promote_vision_to_task` | expandable (vision) | docs/mcp-tools.md (permissions matrix + param reference; no call-trigger sentence, no middleware/scheduler wiring found) | LATENT | - | No -- retained (LATENT, documented + registered; zero code-trigger is not removal authority) | 0 events in local dev sqlite discipline_events; production (Aiven Postgres, 30-day retention) telemetry unavailable -- not queried (Boundaries: no prod DB connection) |
| `propose_behavior_rule` | expandable (behavior_rule) | docs/mcp-tools.md (permissions matrix + param reference; no call-trigger sentence, no middleware/scheduler wiring found) | LATENT | - | No -- retained (LATENT, documented + registered; zero code-trigger is not removal authority) | 0 events in local dev sqlite discipline_events; production (Aiven Postgres, 30-day retention) telemetry unavailable -- not queried (Boundaries: no prod DB connection) |
| `propose_goal` | expandable (proposal) | docs/mcp-tools.md (permissions matrix + param reference; no call-trigger sentence, no middleware/scheduler wiring found) | LATENT | - | No -- retained (LATENT, documented + registered; zero code-trigger is not removal authority) | 0 events in local dev sqlite discipline_events; production (Aiven Postgres, 30-day retention) telemetry unavailable -- not queried (Boundaries: no prod DB connection) |
| `propose_project` | expandable (proposal) | docs/mcp-tools.md (permissions matrix + param reference; no call-trigger sentence, no middleware/scheduler wiring found) | LATENT | - | No -- retained (LATENT, documented + registered; zero code-trigger is not removal authority) | 0 events in local dev sqlite discipline_events; production (Aiven Postgres, 30-day retention) telemetry unavailable -- not queried (Boundaries: no prod DB connection) |
| `query_procedural` | expandable (procedural) | mcpProtocolFull routing table (internal/mcp/tools_onboarding.go appendix; moved out of the injected half by the progressive-disclosure compression, still S1) | USED | - | No -- actively wired (USED) | 0 events in local dev sqlite discipline_events; production (Aiven Postgres, 30-day retention) telemetry unavailable -- not queried (Boundaries: no prod DB connection) |
| `recall` | expandable (procedural) | mcpProtocolFull routing table (internal/mcp/tools_onboarding.go appendix; moved out of the injected half by the progressive-disclosure compression, still S1) | USED | - | No -- actively wired (USED) | 0 events in local dev sqlite discipline_events; production (Aiven Postgres, 30-day retention) telemetry unavailable -- not queried (Boundaries: no prod DB connection) |
| `reconcile_dashboard` | expandable (dashboard) | middleware(autoLogEntry, internal/mcp/middleware_autolog.go/middleware_classify.go) | USED | - | No -- actively wired (USED) | 0 events in local dev sqlite discipline_events; production (Aiven Postgres, 30-day retention) telemetry unavailable -- not queried (Boundaries: no prod DB connection) |
| `reconcile_merged_prs` | expandable (reconcile) | .github/workflows/gtd-reconcile.yml POSTs to /api/tasks/reconcile-merged-prs on every PR merge (same BatchCompleteTasksByPRMatch logic) | USED | - | No -- actively wired (USED) | 0 events in local dev sqlite discipline_events; production (Aiven Postgres, 30-day retention) telemetry unavailable -- not queried (Boundaries: no prod DB connection) |
| `record_outcome` | core | docs/mcp-tools.md (permissions matrix + param reference; no call-trigger sentence, no middleware/scheduler wiring found) | LATENT | - | No -- retained (LATENT, documented + registered; zero code-trigger is not removal authority) | 0 events in local dev sqlite discipline_events; production (Aiven Postgres, 30-day retention) telemetry unavailable -- not queried (Boundaries: no prod DB connection) |
| `resolve_handoff` | core | mcpInstructions routing table (internal/mcp/server.go) / middleware(significantTools, internal/mcp/middleware_autolog.go/middleware_classify.go) / docs/mcp-tools.md explicit 'Call' trigger | USED | - | No -- actively wired (USED) | 0 events in local dev sqlite discipline_events; production (Aiven Postgres, 30-day retention) telemetry unavailable -- not queried (Boundaries: no prod DB connection) |
| `search_atoms` | expandable (atom) | NOT literally redundant today - recall's types param only accepts episodic/semantic/procedural (internal/mcp/tools_procedural.go:79-83), no atoms option. Future-consolidation candidate only; migrating would change response shape and requires extending recall first. Zero S1-S4 evidence of current use. | DEAD-CANDIDATE | recall (types=atoms, NOT YET SUPPORTED) | Tentative -- code-only evidence (see consumers), needs telemetry confirm before H4 removal | 0 events in local dev sqlite discipline_events; production (Aiven Postgres, 30-day retention) telemetry unavailable -- not queried (Boundaries: no prod DB connection) |
| `search_knowledge` | core | mcpInstructions routing table (internal/mcp/server.go) / docs/mcp-tools.md explicit 'Call' trigger | USED | - | No -- actively wired (USED) | 0 events in local dev sqlite discipline_events; production (Aiven Postgres, 30-day retention) telemetry unavailable -- not queried (Boundaries: no prod DB connection) |
| `search_skills` | expandable (skill) | docs/internal/wayneblacktea-2.0-development-prompt.md Phase 4 'Skill Compiler' (no compiler/automation wired -- git log --all has zero P4/skill-compiler commits); docs/mcp-tools.md documents params only, no call-trigger | UNREACHABLE(P4) | - | No -- retained (UNREACHABLE P4 learning tool, explicitly preserved per dispatch boundary) | 0 events in local dev sqlite discipline_events; production (Aiven Postgres, 30-day retention) telemetry unavailable -- not queried (Boundaries: no prod DB connection) |
| `set_session_handoff` | core | mcpInstructions routing table (internal/mcp/server.go) / middleware(autoLogEntry, internal/mcp/middleware_autolog.go/middleware_classify.go) / docs/mcp-tools.md explicit 'Call' trigger / NOTE: Full handoff replace -- NOT a substitute for mark_next_action_done's per-step update. | USED | - | No -- actively wired (USED) | 0 events in local dev sqlite discipline_events; production (Aiven Postgres, 30-day retention) telemetry unavailable -- not queried (Boundaries: no prod DB connection) |
| `set_task_status` | expandable (gtd) | docs/mcp-tools.md (permissions matrix + param reference; no call-trigger sentence, no middleware/scheduler wiring found) / NOTE: NOT equivalent to update_task+complete_task (code-verified): update_task's status enum excludes 'completed' entirely (tools_gtd.go:274-276) so it can never complete a task; complete_task cannot reopen a completed/cancelled task; only set_task_status supports the full reopen matrix and explicitly forbids outcome recording. | LATENT | - | No -- retained (LATENT, documented + registered; zero code-trigger is not removal authority) | 0 events in local dev sqlite discipline_events; production (Aiven Postgres, 30-day retention) telemetry unavailable -- not queried (Boundaries: no prod DB connection) |
| `start_work` | expandable (work_session) | middleware(autoLogEntry, internal/mcp/middleware_autolog.go/middleware_classify.go) / docs/mcp-tools.md explicit 'Call' trigger | USED | - | No -- actively wired (USED) | 0 events in local dev sqlite discipline_events; production (Aiven Postgres, 30-day retention) telemetry unavailable -- not queried (Boundaries: no prod DB connection) |
| `submit_review` | expandable (learning) | docs/mcp-tools.md (permissions matrix + param reference; no call-trigger sentence, no middleware/scheduler wiring found) | LATENT | - | No -- retained (LATENT, documented + registered; zero code-trigger is not removal authority) | 0 events in local dev sqlite discipline_events; production (Aiven Postgres, 30-day retention) telemetry unavailable -- not queried (Boundaries: no prod DB connection) |
| `sync_repo` | expandable (context) | middleware(significantTools, internal/mcp/middleware_autolog.go/middleware_classify.go) | USED | - | No -- actively wired (USED) | 0 events in local dev sqlite discipline_events; production (Aiven Postgres, 30-day retention) telemetry unavailable -- not queried (Boundaries: no prod DB connection) |
| `sync_to_notion` | expandable (knowledge) | docs/mcp-tools.md (permissions matrix + param reference; no call-trigger sentence, no middleware/scheduler wiring found) | LATENT | - | No -- retained (LATENT, documented + registered; zero code-trigger is not removal authority) | 0 events in local dev sqlite discipline_events; production (Aiven Postgres, 30-day retention) telemetry unavailable -- not queried (Boundaries: no prod DB connection) |
| `system_health` | expandable (health) | docs/mcp-tools.md (permissions matrix + param reference; no call-trigger sentence, no middleware/scheduler wiring found) | LATENT | - | No -- retained (LATENT, documented + registered; zero code-trigger is not removal authority) | 0 events in local dev sqlite discipline_events; production (Aiven Postgres, 30-day retention) telemetry unavailable -- not queried (Boundaries: no prod DB connection) |
| `task_checklist_add_item` | expandable (gtd) | docs/mcp-tools.md (permissions matrix + param reference; no call-trigger sentence, no middleware/scheduler wiring found) | LATENT | - | No -- retained (LATENT, documented + registered; zero code-trigger is not removal authority) | 0 events in local dev sqlite discipline_events; production (Aiven Postgres, 30-day retention) telemetry unavailable -- not queried (Boundaries: no prod DB connection) |
| `task_checklist_complete` | expandable (gtd) | code-identical: handleChecklistComplete builds the exact same gtd.UpdateChecklistItemParams{Done:&true} and calls the same s.gtd.UpdateChecklistItem as handleChecklistToggle when done=true/evidence_url omitted (internal/mcp/tools_gtd.go:1374-1425) | DEAD-CANDIDATE | task_checklist_toggle(item_id, done=true) | Tentative -- code-only evidence (see consumers), needs telemetry confirm before H4 removal | 0 events in local dev sqlite discipline_events; production (Aiven Postgres, 30-day retention) telemetry unavailable -- not queried (Boundaries: no prod DB connection) |
| `task_checklist_toggle` | expandable (gtd) | docs/mcp-tools.md (permissions matrix + param reference; no call-trigger sentence, no middleware/scheduler wiring found) | LATENT | - | No -- retained (LATENT, documented + registered; zero code-trigger is not removal authority) | 0 events in local dev sqlite discipline_events; production (Aiven Postgres, 30-day retention) telemetry unavailable -- not queried (Boundaries: no prod DB connection) |
| `traverse_atoms` | expandable (atom) | docs/mcp-tools.md (permissions matrix + param reference; no call-trigger sentence, no middleware/scheduler wiring found) | LATENT | - | No -- retained (LATENT, documented + registered; zero code-trigger is not removal authority) | 0 events in local dev sqlite discipline_events; production (Aiven Postgres, 30-day retention) telemetry unavailable -- not queried (Boundaries: no prod DB connection) |
| `update_project` | expandable (gtd) | docs/mcp-tools.md (permissions matrix + param reference; no call-trigger sentence, no middleware/scheduler wiring found) | LATENT | - | No -- retained (LATENT, documented + registered; zero code-trigger is not removal authority) | 0 events in local dev sqlite discipline_events; production (Aiven Postgres, 30-day retention) telemetry unavailable -- not queried (Boundaries: no prod DB connection) |
| `update_project_status` | expandable (gtd) | middleware(significantTools, internal/mcp/middleware_autolog.go/middleware_classify.go) | USED | - | No -- actively wired (USED) | 0 events in local dev sqlite discipline_events; production (Aiven Postgres, 30-day retention) telemetry unavailable -- not queried (Boundaries: no prod DB connection) |
| `update_skill_from_outcome` | expandable (skill) | docs/internal/wayneblacktea-2.0-development-prompt.md Phase 4 'Skill Compiler' (no compiler/automation wired -- git log --all has zero P4/skill-compiler commits); docs/mcp-tools.md documents params only, no call-trigger | UNREACHABLE(P4) | - | No -- retained (UNREACHABLE P4 learning tool, explicitly preserved per dispatch boundary) | 0 events in local dev sqlite discipline_events; production (Aiven Postgres, 30-day retention) telemetry unavailable -- not queried (Boundaries: no prod DB connection) |
| `update_task` | core | mcpInstructions routing table (internal/mcp/server.go) / middleware(autoLogEntry, internal/mcp/middleware_autolog.go/middleware_classify.go) / NOTE: Cannot set status=completed (enum omits it, tools_gtd.go:274-276) -- set_task_status and complete_task are NOT redundant with this tool. | USED | - | No -- actively wired (USED) | 1 event (local dev sqlite discipline_events, observed_at=2026-07-20T08:33:31.744Z) -- NOT representative of prod 30-day retention window |
| `update_vision_item` | expandable (vision) | docs/mcp-tools.md (permissions matrix + param reference; no call-trigger sentence, no middleware/scheduler wiring found) | LATENT | - | No -- retained (LATENT, documented + registered; zero code-trigger is not removal authority) | 0 events in local dev sqlite discipline_events; production (Aiven Postgres, 30-day retention) telemetry unavailable -- not queried (Boundaries: no prod DB connection) |
| `upsert_project_arch` | core | mcpInstructions routing table (internal/mcp/server.go) / middleware(significantTools, internal/mcp/middleware_autolog.go/middleware_classify.go) / docs/mcp-tools.md explicit 'Call' trigger | USED | - | No -- actively wired (USED) | 0 events in local dev sqlite discipline_events; production (Aiven Postgres, 30-day retention) telemetry unavailable -- not queried (Boundaries: no prod DB connection) |
| `use_skill` | expandable (skill) | docs/internal/wayneblacktea-2.0-development-prompt.md Phase 4 'Skill Compiler' (no compiler/automation wired -- git log --all has zero P4/skill-compiler commits); docs/mcp-tools.md documents params only, no call-trigger | UNREACHABLE(P4) | - | No -- retained (UNREACHABLE P4 learning tool, explicitly preserved per dispatch boundary) | 0 events in local dev sqlite discipline_events; production (Aiven Postgres, 30-day retention) telemetry unavailable -- not queried (Boundaries: no prod DB connection) |

---

## 2. Structural findings that disprove claimed tool equivalences

The SA spec named three claimed equivalences to verify against current source. All
three are **disproven by code** — none of the "replaced-by" tools should be treated as
redundant:

1. **`set_task_status` is NOT `update_task` + `complete_task`.**
   `update_task`'s `status` enum is `pending`/`in_progress`/`cancelled` only
   (`internal/mcp/tools_gtd.go:274-276`) — it structurally cannot set `completed`.
   `complete_task` (`tools_gtd.go:244-253`) auto-seeds a draft outcome and has no
   reopen semantics. Only `set_task_status` (`tools_gtd.go:377-392`) supports the full
   reopen matrix (`completed/cancelled → pending/in_progress`) and its own description
   explicitly forbids calling `record_outcome`/`evaluate_outcome` — a behavior neither
   of the other two tools has or could substitute for.

2. **`mark_next_action_done` is NOT `set_session_handoff`.**
   `handleMarkNextActionDone` (`internal/mcp/tools_session.go:101-122`) mutates a
   single `next_actions[step].status` field on an *existing* handoff row via
   `session.MarkNextActionDone`, leaving `intent`/`context_summary`/other steps
   untouched. `handleSetSessionHandoff` (`tools_session.go:44-83`) always replaces the
   entire handoff record via `session.SetHandoff` and has no per-step selector —
   calling it to mark one step done would require resending the full `next_actions`
   array and risks clobbering sibling steps' status.

3. **`confirm_proposal` is only a *possible* future consolidation into
   `confirm_proposals`, contingent on unresolved parity.**
   `handleConfirmProposals`'s accept path does reuse `acceptProposal` per-id
   (parity with singular), but its reject path calls `proposal.BatchConfirm`
   (`tools_proposal.go:246`) — a different store method than singular's
   `proposal.Resolve` (`tools_proposal.go:317`), with a different error message
   ("proposal not found or already resolved" vs. whatever `BatchConfirm` surfaces per
   item) and a different response shape (`confirmResult{Proposal, Created}` vs.
   `BatchConfirmResult{Results, Accepted, Failed}`). `docs/mcp-tools.md:453` itself
   already documents that `confirm_proposals` is excluded from the classify-middleware
   `significantTools` set even though it "performs the same accept/reject
   materialisation" as `confirm_proposal` — a known, maintainer-flagged asymmetry.
   **Not eligible for consolidation today**; would require unifying the reject path and
   response shape first.

---

## 3. H2 — Deprecation-candidate set (for a future H4 removal pass, NOT this PR)

Three tools are flagged `DEAD-CANDIDATE`. All three are **code-proven, not
telemetry-proven** — per the Threat section below, zero in-repo/local-telemetry
evidence of use is not removal authority on its own. Each needs a production
telemetry confirm (0 calls across the real 30-day retention window) before any H4
removal work begins.

| Tool | Replacement | Confidence | Why |
|---|---|---|---|
| `task_checklist_complete` | `task_checklist_toggle(item_id, done=true)` | **High** | Byte-identical implementation. `handleChecklistComplete` (`internal/mcp/tools_gtd.go:1406-1425`) constructs `gtd.UpdateChecklistItemParams{Done: &true}` and calls `s.gtd.UpdateChecklistItem` — exactly what `handleChecklistToggle` (`tools_gtd.go:1374-1404`) does when called with `done=true` and no `evidence_url`. Zero divergence in stored fields or response shape. |
| `outline_knowledge` | `navigate_knowledge(parent_id=item_id)` | **High** | Byte-identical implementation. `handleOutlineKnowledge` and the non-root branch of `handleNavigateKnowledge` (`internal/mcp/tools_knowledge_nav.go:65-114`) both call `s.knowledge.ListChildren(ctx, id)`, map through the same `navItemFromDB`, and return `jsonText(nav)` verbatim. The only functional difference `navigate_knowledge` has over `outline_knowledge` is supporting an *empty* `parent_id` to list roots — a strict superset. |
| `search_atoms` | `recall` (types=atoms) — **not yet supported** | **Low / tentative** | NOT literally redundant today. `recall`'s `types` param only accepts `episodic`/`semantic`/`procedural` (`internal/mcp/tools_procedural.go:79-83`) — no `atoms` option exists. This matches the SA spec's own hedge ("response differs, migration needed"): consolidating would require (a) extending `recall` to cover the atom domain and (b) a response-shape migration for any caller. Flagged only because it has zero S1–S4 evidence and is a narrower single-purpose search than the rest of the atom/procedural/knowledge search surface — not because a replacement exists today. |

None of the three have been touched by this PR. Any future removal must go through a
dedicated H4 dispatch with production telemetry confirmation, not this read-only pass.

---

## 4. UNREACHABLE(P4) — preserved, not removal candidates

Five tools belong to the Skill domain (`internal/skill`), registered and fully
documented, but with **zero** S1–S4 wiring signal:
`extract_skill`, `search_skills`, `use_skill`, `update_skill_from_outcome`,
`list_relevant_skills`.

Evidence this is a genuine "ahead of its automation" phase gap, not dead code:
`docs/internal/wayneblacktea-2.0-development-prompt.md:145,557` names these exact four
primitive tools under **"Phase 4: Skill Compiler"** and states the missing piece is
"a compiler that proposes skills from repeated verified outcomes" — i.e. the tools
exist as manually-callable primitives, but the automation that was planned to invoke
them systematically has not shipped. `git log --oneline --all | grep -iE
"P3|P4|P5|skill.compiler"` returns **zero commits** — confirming Phase 3/4 sprints
never happened (compare: P0/P1/P2 and P6 sprints are all present in `git log
--oneline -20`). Note `assemble_context` (a `USED` tool) internally calls
`skill.Search` directly (`internal/contextpack/retrieval.go:298-301`) for its own
context-ranking purposes — this is a *different* code path than the `search_skills`/
`list_relevant_skills` MCP tool handlers, so it does not constitute S1–S4 evidence for
those two tools, but it does mean skill *data* is already live and queried, just not
through these specific tool entry points.

**Per dispatch boundary: all five are explicitly preserved, not deprecation candidates.**

---

## 5. H3 — Health-report design inputs

Inputs a future `system_health`-style deprecation/usage report should surface, based
on what this inventory found to be missing or fragile:

- **Per-tool call counter with the real 30-day window**, not a proxy. `discipline_events`
  already has `tool_name`, `observed_at`; a `GROUP BY tool_name` over the retained
  window is the correct query — this manifest could not run it against production
  (Boundaries), so H3 should productionize exactly the query used here
  (`SELECT tool_name, COUNT(*), MIN(observed_at), MAX(observed_at) FROM
  discipline_events GROUP BY tool_name`) as a `system_health` sub-report or a new
  read-only MCP tool, gated the same way `system_health` already is.
- **Distinguish `is_mutating=false` tools from zero telemetry.** `discipline_events`
  only records tool calls that pass through `disciplineMiddleware()` — verify (a
  follow-up task, not done here) whether read-only tools are recorded at all, or only
  `discipline.MutatingTools` members. If reads are never recorded, a health report
  needs a second counter (e.g. request logs / access logs) to avoid conflating "no
  read-tool telemetry infra" with "tool never called."
- **Known-unknowns flag per tool**: surface, for every `LATENT`/`DEAD-CANDIDATE` row,
  the same caveat this manifest states inline — "zero calls observed is not removal
  authority; unknown external MCP clients and failed/errored calls are invisible to
  this signal." A health report that silently ranks tools by call count without this
  caveat will mislead a future H4 pass into removing tools used only by, e.g., the
  Discord bot or a CLI script that happens not to hit the local/observed workspace.
- **Track the S1–S4 "wiring class" as a first-class column**, not just a raw count —
  this manifest's methodology (§0) proved that code-level wiring signals
  (`mcpInstructions`, middleware switches, docs "Call" triggers, scheduler/CI
  cross-calls) are available *today* without any telemetry and already separate 36
  tools into `USED` with zero production data. A health report should compute S1–S4
  automatically (they're all static, greppable code facts) and treat production
  telemetry as a *confirming* signal layered on top, not the only signal.
- **`confirm_proposal`/`confirm_proposals` parity gap** (§2.3) should be a tracked
  action item feeding into whatever "candidate consolidation" list a health report
  produces, distinct from the DEAD-CANDIDATE list in §3 — it is not deprecation-eligible
  today, but it is a known design debt the SA spec explicitly asked to preserve
  visibility on.

---

## 6. Threats / Limitations

- **Unknown external MCP clients.** This repo's MCP server is reachable over stdio
  (`wbt mcp`) and HTTP (`/mcp`); any client that has ever connected — Discord bot,
  ad-hoc scripts, a different machine's Claude Code — is invisible to a purely
  in-repo grep. A tool with zero S1–S4 signal and zero local telemetry may still be in
  active use by a client this analysis cannot see.
- **Telemetry is incomplete and short-window even in production.** `discipline_events`
  has a 30-day TTL (`build/Taskfile.yml discipline-prune`, `internal/discipline/discipline.go:11`)
  — "full history" means 30 retained days, not lifetime. A tool used quarterly (e.g.
  a seasonal reflection/reporting tool) would show zero calls in any given 30-day
  window without being dead.
  Additionally, only **successful** calls are persisted (per SA spec finding) —
  failed/errored invocations, and (unverified in this pass) possibly read-only
  `is_mutating=false` calls, may not be recorded at all. See H3 above.
- **This manifest's own telemetry sample is non-representative.** The 3-row local
  dev SQLite snapshot used for best-effort telemetry in §"Telemetry" is manual
  developer testing on one machine, not the production Aiven Postgres instance. It was
  used only to demonstrate the query shape and was explicitly not treated as evidence
  for or against any tool's classification.
- **Classification is code-proven, not usage-proven, for `DEAD-CANDIDATE` rows.**
  Each of the three (§3) is flagged with an explicit confidence level and an explicit
  "needs telemetry confirm before H4 removal" caveat in its Deprecation-eligible
  column. None should be removed on the basis of this document alone.
- **Zero calls is NOT removal authority** (SA spec, verbatim). This principle is
  applied throughout: every `LATENT` and `UNREACHABLE(P4)` tool is explicitly retained,
  not flagged for future removal, regardless of its S1–S4/telemetry signal.

---

## 7. Verification

- `git diff --stat` for this change: only this file is added — zero tool
  registration/schema/description touched (verify below).
- `go test ./internal/mcp/... -run TestMCPServer_AllRegisteredToolsClassified -v`:
  PASS — structural parity between `ListTools()` and
  `discipline.MutatingTools ∪ discipline.DeliberatelyExcludedTools` holds at 92/92
  (`expand_tools` is classified in `DeliberatelyExcludedTools`: it mutates only
  process-local, per-session `tools/list` visibility, never a Store).
