-- SQLite parity for migration 000075 (outcomes.updated_at).
-- See migrations/000075_outcomes_updated_at.up.sql for the Postgres source
-- of truth and full design rationale.
--
-- Dialect differences: TIMESTAMPTZ -> TEXT (RFC3339), matching created_at's
-- own dialect mapping in migration 000054. SQLite's ALTER TABLE cannot add
-- a NOT NULL constraint to an existing non-empty table without a full
-- 12-step table rebuild (overkill for a personal-scale project), so this
-- column stays nullable at the schema level. internal/storage/sqlite/
-- outcome.go's CreateOutcome, FinalizeDraft, AND SeedDraft (PR #152 round 4
-- Major: SeedDraft was originally missed here — it is a third writer of this
-- table, not just CreateOutcome/FinalizeDraft) all supply an explicit value
-- on every write (mirrors created_at's own explicit-value-on-every-INSERT
-- pattern in that file, which similarly never leans on a DB-level DEFAULT
-- for correctness), so the column is never actually NULL in practice once
-- this migration has run.
--
-- Backfill: existing rows get updated_at = created_at, same rationale as
-- the Postgres migration.
ALTER TABLE outcomes ADD COLUMN updated_at TEXT;

UPDATE outcomes SET updated_at = created_at WHERE updated_at IS NULL;
