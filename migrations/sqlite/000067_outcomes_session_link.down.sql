-- 000067_outcomes_session_link.down.sql (SQLite twin)
-- SQLite 3.35+ supports DROP COLUMN; drop work_session_id from outcomes.
-- The applyColumnUpgrades() idempotent path in db.go Open() does not run
-- this; down migrations are only applied via the migrate-down Taskfile target.
ALTER TABLE outcomes DROP COLUMN work_session_id;
