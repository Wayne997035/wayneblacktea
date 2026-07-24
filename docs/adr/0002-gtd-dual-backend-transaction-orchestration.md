---
status: accepted
---

# GTD dual-backend transaction orchestration: local exception to "no Service/Repository split"

`internal/gtd/store.go` (pgx/Postgres) and `internal/storage/sqlite/gtd.go` (database/sql/SQLite) each implement `BeginTask` and `DeleteTask` in full — ~100 lines each, same six logical steps (idempotency check → assignee gate → transaction → guarded conditional write → activity log → commit), duplicated because they talk to genuinely different databases. This is not hypothetical duplication: SQLite is not a test stand-in here — per `CLAUDE.md`, it is the backend anyone else installing wbt runs (Postgres/Aiven is this deployment's own production choice). A bug fixed in one implementation and missed in the other is a real correctness risk for other installs, not just this one.

We introduce a local exception to `CLAUDE.md`'s "no Service/Repository split" principle, scoped narrowly: the transaction *orchestration* (step sequencing, error wrapping, commit/rollback lifecycle, idempotency-then-gate-then-guarded-write control flow) is unified into one dialect-agnostic function per method. Dialect-specific SQL text is NOT unified — placeholder syntax (`$1` vs `?1`), timestamp handling (`NOW()` vs a precomputed Go timestamp), and the guard-blocked signal (`pgx.ErrNoRows` from a sqlc-generated conditional UPDATE vs `RowsAffected() == 0` from raw SQL) all stay backend-specific behind a narrow `execer`-style adapter per method. Each adapter translates its native signal into one shared boolean/result the orchestration understands; the orchestration never sees `pgx` or `database/sql` types directly.

This follows the DEEPENING.md ports-and-adapters pattern: two real production adapters (not one adapter posing as a seam) justify the seam. `gtd.RequireAssigneeForInProgress` (`internal/gtd/actor.go:127`), already shared as a pure domain-rule function, is the existing proof that extracting dialect-agnostic logic out of this pair works.

## Considered Options

- **Do nothing, accept the duplication**: rejected — the two ~100-line copies (including copy-pasted doc comments) are exactly the "bug fixed in one place, not the other" failure mode `CLAUDE.md`'s rule exists to prevent in the single-backend case; here the multi-backend reality defeats that rule's own premise.
- **Also unify the SQL text via a small internal query-abstraction layer**: rejected. Would eliminate the remaining duplication (the actual SQL statements), but amounts to building a lightweight query-builder/ORM for a personal-scale project — high engineering cost, and SQL-generation bugs are a harder-to-spot failure class than duplicated-but-readable SQL strings. Same reasoning that ruled out heavy codegen for the MCP tool arg structs in [ADR 0001](0001-mcp-tool-handler-seam.md).
- **Start with `DeleteTask` (higher risk, higher payoff first)**: rejected for the first cut. `DeleteTask` has more moving parts (workspace pre-check + 4 cascade-cleanup statements + final sqlc delete) than `BeginTask` (uniform 6-step shape on both backends, the only real dialect difference is the guard signal). Validating the orchestration/adapter shape on the simpler case first is lower-risk; the pattern then transfers to `DeleteTask`.

## Consequences

- `BeginTask` is refactored first; `DeleteTask` follows once the orchestration/adapter shape is validated — this ADR's scope for the first PR is `BeginTask` only.
- Existing dual-backend parity tests (`internal/gtd/store_postgres_test.go`, `internal/storage/sqlite/gtd_test.go`) are **not** deleted or replaced — they verify real per-backend SQL correctness against a real database, which orchestration-level tests (using a fake adapter) cannot substitute for. New orchestration-level tests are added *in addition*, covering the shared control flow in isolation. This is the opposite of ADR 0001's "replace, don't layer" call for the MCP tool seam — there the old tests covered implementation detail made obsolete by the new interface; here the old tests cover real per-dialect behavior that remains load-bearing regardless of how much control flow is shared.
- This exception is scoped to dual-backend transactional methods in `internal/gtd`/`internal/storage/sqlite` specifically. It does not license a general Service/Repository layer elsewhere in the codebase — `CLAUDE.md`'s rule stands everywhere a single backend has a single owner.
