-- 000079_task_area.down.sql (SQLite twin)
-- [F186-01]
--
-- SQLite 3.35+ supports DROP COLUMN (same note as 000076/000078's SQLite
-- down twins — not a concern on this repo's target platforms).
--
-- The backfill is not reversed: every row's pre-migration area was "no area
-- at all", which dropping the column restores exactly.

DROP INDEX IF EXISTS idx_tasks_area_status;

ALTER TABLE tasks DROP COLUMN area;

DROP TABLE IF EXISTS task_areas;
