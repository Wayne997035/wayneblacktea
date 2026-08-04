-- 000074_outcomes_supersession.down.sql
DROP INDEX IF EXISTS idx_outcomes_one_open_draft;
DROP INDEX IF EXISTS idx_outcomes_supersedes_id;
ALTER TABLE outcomes DROP COLUMN IF EXISTS supersedes_id;
