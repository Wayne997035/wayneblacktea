# Historical Migration Exceptions

Migrations are immutable after merge. If schema changes are needed, add a new
numbered migration instead of editing a historical one.

Known exception:

- `000011_backfill_workspace_id.up.sql`: manual incident-recovery backfill that
  intentionally uses `psql` metacommands and is skipped by pgx-based test
  migration runners.

- **2026-07-18, E1 (migrations/sqlite/ becomes a real golang-migrate runner
  for the first time)** — two SQLite twin migrations edited, both approved by
  team-lead ruling after `migrations/sqlite/*.sql` was actually executed for
  the first time ever (previously `internal/storage/sqlite/schema.sql` was
  the sole runtime authority; these twin files existed only as documentation
  that nothing had ever replayed). Divergence set with any real DB = empty
  in both cases, since no DB has ever run either statement:

  - `migrations/sqlite/000028_repos_composite_unique.up.sql`: removed
    `DROP INDEX IF EXISTS sqlite_autoindex_repos_1;`. SQLite unconditionally
    creates `sqlite_autoindex_<table>_1` for any table's first UNIQUE/PRIMARY
    KEY constraint (`repos.id TEXT PRIMARY KEY` is `repos`' first, and only,
    such constraint by column order), and SQLite refuses to `DROP INDEX` any
    autoindex backing a UNIQUE/PRIMARY KEY constraint ("index associated with
    UNIQUE or PRIMARY KEY constraint cannot be dropped") — confirmed
    empirically 3 ways (column-level UNIQUE, named unique index, PK-only
    table). The line always targeted `repos`' PK autoindex, never a
    name-uniqueness index; the migration's real intent (drop the old
    name-only unique index, replace with the per-workspace composite one) is
    fully preserved by the remaining `DROP INDEX IF EXISTS idx_repos_name_unique;`
    and the `CREATE UNIQUE INDEX idx_repos_workspace_name_unique` that follow,
    both untouched.

  - `migrations/sqlite/000064_embedding_provider_marker.up.sql`: removed the
    `UPDATE decisions SET embedding_provider = 'hashed', embedding_dim = 32
    WHERE embedding IS NOT NULL AND embedding_provider IS NULL;` statement
    (the `session_handoffs` and `project_status_snapshots` UPDATE statements
    are untouched). `decisions` has never carried a raw `embedding` column in
    `schema.sql` — migration `000020_vector_recall.up.sql` added it, but
    `000026_drop_fk_constraints.up.sql`'s table-rebuild dance (left
    unmodified) never carried it into `decisions_new`, so on real replay the
    column is already gone by the time `000064` runs and the `UPDATE`
    unconditionally errors `no such column: embedding`. The net effect of
    leaving `000026` untouched is that the final schema state already matches
    `schema.sql` exactly (no `decisions.embedding` column) — this single
    `UPDATE` deletion is a complete fix, no corrective migration needed
    afterward.

  Both edits are dead-on-arrival statements from files that were, until E1,
  never-executed documentation. Team-lead ruling 2026-07-18 (E1
  golden-equivalence review) authorized both as an explicit exception to the
  immutability rule on this basis. See `internal/storage/sqlite/golden_schema_test.go`
  (`TestGoldenSchemaEquivalence`) for the acceptance test these fixes make pass.

- Stale header comments inside already-merged `migrations/sqlite/` files (for
  example `000026_drop_fk_constraints.up.sql`, whose header still claims the
  SQLite runtime "does NOT consume migrations/sqlite/") predate the E1
  migration-runner change and are intentionally left as-is: merged migrations
  are immutable, and comment-only drift does not affect execution. This note
  is the canonical correction — since E1 (2026-07-18), `migrations/sqlite/`
  IS executed by golang-migrate at `Open()`.

- **2026-07-19, comment errata in
  `migrations/000068_work_session_evidence_indexes.up.sql`** (merged via
  f2e9829; file immutable, comment-only drift, this note is the canonical
  correction — same convention as the stale-header note above; r3-db review
  Minor ×2):

  - The F4 justification claims the two partial indexes "back
    `get_work_session_trace` / `record_outcome` reverse lookups". Neither
    query path exists: `get_work_session_trace` resolves the session by its
    `id` primary key, and `SetOutcomeLink` updates by `id + workspace_id`
    (`internal/worksession/store.go` `SetOutcomeLink`, `UPDATE … WHERE id =
    $2 AND workspace_id = $3`). Read `idx_work_sessions_outcome_id` /
    `idx_outcomes_work_session_id` as forward-compatible hardening for the
    outcome↔work-session reverse lookups planned by the wbt-2.0
    learning-loop work, not as serving a query that already runs.

  - The `context_pack_id` paragraph cites
    `internal/mcp/tools_worksession.go:198`, but that line is
    `parseTaskIDsFromField`. The supporting fact lives at the
    `assembleStartWorkContext` comment ("context_pack_id stays NULL always:
    Phase 2 does not persist Packs yet"). Line numbers into Go source drift —
    trust the named function/comment, not the cited line.
