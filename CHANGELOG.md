# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added — Full-repo audit

- Dead code removal: unused SQL queries, orphan files, and duplicated helpers consolidated across several commits; `internal/storage/sqlite/schema.sql`'s exported `Schema()` accessor removed (zero remaining callers).
- REST API: 13 unused endpoints removed.
- Retention: three new daily pruners added — `activity_log` (365 days), `project_status_snapshots` (180 days, Postgres-only), and `session_handoffs` (365 days, resolved rows only; open handoffs are never pruned). See `docs/retention-policy.md`.
- SQLite: schema evolution now runs through golang-migrate against the embedded `migrations/sqlite/*.sql` set (baseline `000012_sqlite_baseline` through `000072`) instead of the old `schema.sql`-applied-idempotently mechanism. A pre-existing DB with no `schema_migrations` table is adopted by Force-stamping it at the latest version rather than replaying history; `schema.sql` is retired to a test-fixture-only role.
- Docs: internal planning docs archived to `docs/internal/`, `AGENTS.md` and `docs/ci-secrets.md` rewritten to match the workflows that actually exist, and the `docs/operations.md` legacy-000011 backfill section corrected.

### Added — wbt-2.0 P2 Action Lifecycle

- Work session evidence chain: `finish_work` accepts an `evidence` array (command output, PR/CI links, Railway deploy logs, manual notes); `get_work_session_trace` reads it back to reconstruct what was actually verified.
- Deferred-task semantics and outcome linkage wired into the work-session lifecycle, plus retention indexes for the new evidence tables.

### Added — wbt-2.0 P1 Context Pack MVP

- `assemble_context` MCP tool: a deterministic, budget-bounded context pack assembled from 10 domain stores (decisions, knowledge, procedural memories, atoms, outcomes, and related session history) — read-only, with additive ranking, per-type caps, and rune-budget trimming.
- `knowledge.SearchReadOnly` added (both backends) so context assembly never bumps recall stats — keeps the tool genuinely side-effect-free.

### Added — wbt-2.0 P0 doc sprint

- `docs/mcp-tools.md`, `docs/architecture.md`, `docs/retention-policy.md`, `DESIGN.md`, and `security.md` synced to the code as it stood at that point (88 MCP tools documented, scheduler jobs 3→19, bounded contexts 7→22).

### Added — Phase 2 install simplification

- `wbt setup`: one-command install. Reads/creates global config, ensures SQLite directory, resolves the HTTP port, reclaims it if occupied, spawns `wayneblacktea-server` in the background via `nohup` (PID file at `$XDG_STATE_HOME/wayneblacktea/server.pid`), polls `/health`, then registers the HTTP MCP transport with Claude Code via `claude mcp add`. Supports `--port`, `--no-mcp`, `--mcp-name`, `--server-bin` flags. Reuses an already-running healthy instance instead of double-spawning.
- `wbt status`: reports whether the background server is running and healthy. Supports `--format plain` (default, one-line summary) and `--format json` (machine-readable shape with `pid` / `port` / `transport` / `healthy` / `pid_file` / `started_at`).
- `wbt stop`: terminates the background server identified by the PID file and removes the PID file. Idempotent.
- `wbt restart`: `wbt stop` followed by `wbt setup`.
- `internal/lifecycle` package: PID file management, port reclamation (`KillOccupier`), `NohupSupervisor` for background process spawning, all unit-tested against real processes / sockets.
- README: install section rewritten around `wbt setup` (was `wbt init`); sister-command table added.
- `docs/install.md`: "Quick start" rewritten with `wbt setup` flow + expected checkmark output; new "Migration from earlier wbt" subsection documents `wbt init` as deprecated alias.
- `scripts/install.sh`: trims to download + verify + extract + write `.env`; prints "next: run `wbt setup`" hint instead of inlining MCP registration logic. Avoids duplicating `wbt setup` orchestration in two places.

### Deprecated — Phase 2 install simplification

- `wbt init`: deprecated alias for `wbt setup`. Still runs, prints a stderr deprecation notice. Will be removed in a future major version.

### Added — Sprint 0517-P1

- Dashboard: new `/proposals` page lists pending proposals for review.
- Dashboard: `HandoffCard` now displays the `next_actions` field from session handoffs.
- GTD Store interface: `GetTaskByID(ctx, uuid) (*gtd.Task, error)` added to both SQLite and Postgres backends.
- Session rows now carry `branch_name`, `pr_url`, `commit_shas`, and `next_actions` fields.
- `list_decisions` MCP tool: fixed a fallthrough bug in the switch statement that could return incorrect results.
- `cmd/server`: HTTP security response headers added (`X-Content-Type-Options`, `X-Frame-Options`, and others).
- Build: `go test` now runs with `-p 4` for parallel package execution.

### Fixed — Sprint 0517-P1

- `posttooluse` handler: `actor`, `action`, and `notes` fields are now sanitized before DB write.
- `autolog` handler: input fields are now validated before DB write.
- `autolog` middleware: `project_id` is now correctly propagated from task lookup context.

### Added — Phase A (schema)
- 10 domain tables now carry a nullable `workspace_id` column with a partial
  index (`WHERE workspace_id IS NOT NULL`) for future per-workspace scoping.
- `tasks.importance` (SMALLINT 1-3, CHECK constrained) and `tasks.context`
  (TEXT) for richer GTD entries.
- `pending_proposals` table for agent-originated entities awaiting user
  confirmation (CHECK constraints on `type` and `status`, partial indexes on
  pending status and workspace).

### Added — Phase B1 (proposal gate + GTD richness)
- New MCP tools: `propose_goal`, `propose_project`, `list_pending_proposals`,
  `confirm_proposal`. `confirm_proposal action='accept'` materialises the
  entity and resolves the proposal in a single transaction.
- `add_task` accepts `importance` (1-3) and `context` parameters.
- `add_knowledge` (MCP and HTTP) auto-creates a pending concept proposal
  for review-eligible types (`article`, `til`, `zettelkasten`); MCP returns
  the proposal ID alongside the knowledge item.
- `internal/proposal` bounded context: Store with `WithTx` for atomic
  materialisation, opaque JSONB payload, idempotent `Resolve`.

### Added — Phase B2 (workspace plumbing)
- `internal/runtime` package exposing `WorkspaceIDFromEnv` and
  `UserIDFromEnv`. Empty `WORKSPACE_ID` preserves legacy behaviour.
- All seven domain stores (`gtd`, `decision`, `session`, `workspace`,
  `knowledge`, `learning`, `proposal`) now hold the workspace at init and
  apply it to every read and write. `WithTx` preserves the scope.
- All `sql/queries/*.sql` use the `sqlc.narg('workspace_id')` pattern so
  NULL disables filtering and a UUID enforces strict scoping.
- `cmd/server`, `cmd/mcp`, `cmd/seed` read `WORKSPACE_ID` at startup.

### Added — Phase C (storage interface lift + SQLite v1)
- `internal/storage` package with `Backend` enum, `BackendFromEnv`,
  `EnsureSupported`, and `ResolveFromEnv`.
- Per-domain `StoreIface` declared in `internal/<domain>/iface.go`. Each
  concrete `*Store` is checked at compile time. SQLite-backed stores can
  satisfy the same surface when implemented.
- `internal/storage/sqlite` package: pure-Go (modernc.org/sqlite, no
  CGo) backend.
  - `sqlite.Open` opens a file or `:memory:` DB and applies the embedded
    schema idempotently (`CREATE TABLE IF NOT EXISTS …`).
  - `sqlite.GTDStore` fully satisfies `gtd.StoreIface`: create / list /
    update / delete goals, projects, tasks, and activity log, including
    workspace scoping, importance/context, and the WeeklyProgress query.
    10 unit tests pass against in-memory SQLite.
  - Other six domain stores (decision / session / workspace / knowledge /
    learning / proposal) are deferred to follow-up commits — the schema
    contains the tables but no Go-side `Store` ships in this commit.
- Entry-point binaries currently still fail-fast on
  `STORAGE_BACKEND=sqlite`. Lifting that gate happens once the remaining
  six stores ship.

### Added — Phase D (open source readiness)
- README.md with architecture diagram, env var table, and phase summary.
- LICENSE (MIT).
- CONTRIBUTING.md with workflow + code style.
- `.goreleaser.yml` cross-compiling `wayneblacktea-server` and
  `wayneblacktea-mcp` for macOS/Linux on amd64 and arm64.

[Unreleased]: https://github.com/Wayne997035/wayneblacktea/commits/master
