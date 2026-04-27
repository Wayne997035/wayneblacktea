# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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

### Known limitations
- The SQLite backend is **not yet implemented**. Setting
  `STORAGE_BACKEND=sqlite` errors at startup. Tracked as a follow-up task.
- Phase A migrations are not auto-applied. After upgrading, run each
  `migrations/000008..010_*.up.sql` against your live database.
- Existing rows have `NULL` workspace_id; setting `WORKSPACE_ID` will hide
  them. A backfill migration scaffold is at
  `migrations/000011_backfill_workspace_id.sql` (commented; customise
  before running).

[Unreleased]: https://github.com/waynechen/wayneblacktea/commits/master
