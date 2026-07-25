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

- **2026-07-25, E2 (`000073_decision_source` edited AFTER it had already run
  against production)** — unlike E1, the divergence set here is **NOT empty**:
  production really did execute the pre-edit body. Read this entry before
  drawing any conclusion about how a given `decisions.source` value in
  production was assigned.

  Sequence of events:

  1. `000073_decision_source` shipped on branch
     `feature/7-25-p3-0-decision-provenance` (commit `91097ca`) with
     `ALTER TABLE decisions ADD COLUMN IF NOT EXISTS source …` followed by
     three **unbounded** backfill `UPDATE`s (no `created_at` predicate).
  2. Team-lead applied that body to production on **2026-07-25 06:49 UTC**
     (`migrate … up` → `73/u decision_source`), moving Aiven Postgres from
     `schema_migrations` version 68 → 73, to unblock `qa-seed` (which reads
     prod with branch code and therefore required the column to exist).
     Post-apply audit of all 838 rows: **auto=505, manual=333**.
  3. Round-1 security review then found that **all three** unbounded
     predicates match `context` / `rationale`, which are caller-supplied on
     every manual write path — so a replay of that body could silently
     reclassify a caller-crafted `manual` row as `auto`. (Round-2 review
     reproduced this on a clean Postgres 17: under the `91097ca` body, crafted
     rows matching predicate 1, 2 **and** 3 all flipped to `auto`.) Commit
     `0518eeb` fixed it **in place**, adding
     `created_at < '2026-07-25T07:00:00Z'` to all three `UPDATE`s and changing
     `ADD COLUMN IF NOT EXISTS` to plain `ADD COLUMN` so a replay fails loudly.
     The same fix was applied to the SQLite twin
     `migrations/sqlite/000073_decision_source.up.sql` in that commit. On the
     SQLite side the divergence set is **limited to throwaway databases**: no
     persistent SQLite environment ever ran the pre-edit body (the in-tree dev
     DB `./wayneblacktea.db` was still at version 72), but the disposable
     `qa-seed` scratch databases created before the fix (`/var/folders/.../
     wbt-qa-seed-20260725-0647*.db`, `…-0650*.db`) did reach version 73 on it.
     Those are per-run temp files used only for browser QA and are discarded,
     so they carry no forensic weight — but "the SQLite twin never executed
     anywhere" would be **false**, and this entry should not claim it. Only
     the Postgres side has a divergence that matters.

  **Therefore the file's current content is NOT what production ran.**
  Production ran the unbounded version exactly once. Its existing
  `decisions.source` values were assigned by the three predicates *without*
  any time bound — which, for the rows **present at apply time**, produces the
  same result either way. That is not merely an audit result but a structural
  guarantee: `created_at` is server-assigned (`CreateDecision` never binds it,
  so it takes `DEFAULT NOW()`) and no production path ever `UPDATE`s it, so a
  row existing at 06:49 UTC cannot carry a `created_at` at or after 07:00 UTC.
  The 838-row audit (auto=505 / manual=333) is corroboration, not the load
  bearing argument.

  One caveat a future investigator should know: the cutoff was rounded **up**
  from the 06:49 UTC apply time to 07:00 UTC, so rows written in that
  eleven-minute window came from post-P3.0a code (which binds `Source`
  explicitly) and were **not** part of prod's original backfill, yet they do
  fall inside the range the current file would reclassify. This can only bite
  on a prod `down`+`up` cycle — the one scenario this entry exists to inform.
  The window is closed and in the past; a row would additionally need
  predicate-matching text to be affected.

  Why editing in place was chosen over a follow-up `000074`: golang-migrate
  tracks version numbers, not content hashes (`schema_migrations` holds only
  `(version, dirty)`), and production is already stamped at 73, so it will
  never replay this file. A corrective `000074` could carry fix-up `UPDATE`s
  for already-misclassified rows — what it *cannot* do is change what a
  **fresh** environment executes at step 73, which would leave the dangerous
  unbounded version as the one every new install runs. That is the decisive
  argument for editing in place.

  Process note for next time: when a not-yet-merged migration is applied to a
  real database ahead of merge, add the entry here **at time of deploy**,
  not reconstructed afterwards from a review finding. Flagged by round-2
  `db-inspector` review, which judged the SQL itself sound and this
  documentation gap the only blocking issue **it found** (round-2 security
  review separately left non-blocking findings; this line is not a repo-wide
  all-clear).
