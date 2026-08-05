-- Migration 000075: outcomes.updated_at — audit trail for in-place
-- FinalizeDraft writes (append-semantics redesign, follow-up to 000074's
-- supersession scheme).
--
-- FinalizeDraft (internal/outcome/store.go / internal/storage/sqlite/
-- outcome.go) is the only in-place UPDATE this table permits (a
-- result='unknown' draft being enriched or finalized — guarded by
-- `WHERE result = 'unknown'`, see migration 000074's comment). Before this
-- migration, that UPDATE left no trace at all: created_at never changes,
-- and no other column recorded when a row was last modified. Combined with
-- FinalizeDraft's append-only merge semantics (notes append, metrics
-- new-key-only, related_rule_ids union, work_session_id set-once — see
-- internal/outcome/lifecycle.go), a caller inspecting a draft had no way
-- to tell whether — or when — it had been enriched since creation. This
-- column closes that gap: every actual FinalizeDraft write bumps it;
-- no-op paths (draft preserved, idempotent replay) leave it untouched.
--
-- Backfill: existing rows get updated_at = created_at. This is the
-- correct semantic value, not a placeholder — every row that predates
-- this migration has, by definition, never been modified in place
-- (FinalizeDraft is the only writer capable of an in-place update, and
-- this column is the first thing that would ever record such a write), so
-- "last modified at creation time" is literally true for all of them.
--
-- No FOREIGN KEY per CLAUDE.md red-line #9 (not applicable — plain scalar
-- column, no reference to another row).
ALTER TABLE outcomes ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ;

UPDATE outcomes SET updated_at = created_at WHERE updated_at IS NULL;

ALTER TABLE outcomes ALTER COLUMN updated_at SET NOT NULL;
ALTER TABLE outcomes ALTER COLUMN updated_at SET DEFAULT NOW();
