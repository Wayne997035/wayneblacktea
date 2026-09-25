-- 000082_index_parity.up.sql
-- [F0925-15] SQLite-only migration (Postgres is already the target shape;
-- no migrations/000082_*.sql twin exists — see the numbering precedent set
-- by migrations/sqlite/000069-000072 and 000077, which are also SQLite-only).
--
-- Realigns 3 SQLite indexes whose shape drifted from their Postgres
-- counterpart back to being textually identical to PG. Merged migration
-- files are immutable (backend-security-design.md §6.4), so each fix is a
-- DROP INDEX + CREATE INDEX pair rather than an edit to the migration that
-- originally created the index.
--
-- #1: idx_decisions_task_id — PG (migrations/000048_decisions_task_id.up.sql:7)
-- is a partial index (WHERE task_id IS NOT NULL); SQLite's twin
-- (migrations/sqlite/000048_decisions_task_id.up.sql:7) was not. Every
-- non-test SQLite query against this column is an equality filter
-- (internal/storage/sqlite/decision.go: WHERE task_id = ?1), which a
-- partial index on "task_id IS NOT NULL" still satisfies.
--
-- DROP before CREATE for all 3 below: CREATE INDEX IF NOT EXISTS only checks
-- whether the NAME already exists, not the definition — since all 3 names
-- already exist (from earlier migrations), CREATE INDEX IF NOT EXISTS alone
-- would silently no-op and leave the old shape in place.
DROP INDEX IF EXISTS idx_decisions_task_id;
CREATE INDEX IF NOT EXISTS idx_decisions_task_id
    ON decisions(task_id)
    WHERE task_id IS NOT NULL;

-- #2: idx_work_sessions_workspace_id — PG (migrations/000021_work_sessions.up.sql:46-47)
-- is NOT partial; SQLite's twin (rebuilt by migrations/sqlite/000026_drop_fk_constraints.up.sql:172-173)
-- added a "WHERE workspace_id IS NOT NULL" PG never had. Dropping the
-- partial clause only widens coverage (indexes NULL rows too); every
-- non-test query already filters WHERE workspace_id = ?, unaffected.
DROP INDEX IF EXISTS idx_work_sessions_workspace_id;
CREATE INDEX IF NOT EXISTS idx_work_sessions_workspace_id
    ON work_sessions(workspace_id);

-- #3: idx_work_sessions_repo_name — PG (migrations/000021_work_sessions.up.sql:49-50)
-- orders created_at DESC; SQLite's twin (migrations/sqlite/000026_drop_fk_constraints.up.sql:174-175)
-- has no DESC. Adding DESC only helps ORDER BY created_at DESC queries use
-- the index without a sort step; equality/prefix lookups are unaffected.
DROP INDEX IF EXISTS idx_work_sessions_repo_name;
CREATE INDEX IF NOT EXISTS idx_work_sessions_repo_name
    ON work_sessions(workspace_id, repo_name, created_at DESC);
