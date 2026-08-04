-- 000074_outcomes_supersession.down.sql (SQLite twin)
-- SQLite 3.35+ supports DROP COLUMN; drop supersedes_id from outcomes.
-- Down migrations are only applied via the migrate-down Taskfile target.
DROP INDEX IF EXISTS idx_outcomes_one_open_draft;
DROP INDEX IF EXISTS idx_outcomes_supersedes_id;
ALTER TABLE outcomes DROP COLUMN supersedes_id;
