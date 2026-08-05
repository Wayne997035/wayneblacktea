-- 000074_outcomes_supersession.down.sql
-- Note: the dedup DELETEs in the up migration are not reversible (the
-- discarded duplicate 'unknown' rows are gone) — this is expected for a
-- down migration cleaning up a forward schema change, not a data-loss bug.
DROP INDEX IF EXISTS idx_outcomes_one_open_draft;
ALTER TABLE outcomes DROP COLUMN IF EXISTS supersedes_id;
