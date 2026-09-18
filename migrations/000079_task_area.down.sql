-- 000079_task_area.down.sql
-- [F186-01]
--
-- Drop order is the reverse of up: the index goes with the column it covers,
-- and task_areas is dropped last because tasks.area is the only thing that
-- gives it meaning.
--
-- The backfill is NOT reversed. Every row's pre-migration area was, by
-- definition, "no area at all" — dropping the column already restores that
-- exactly. There is no separate state to undo.

DROP INDEX IF EXISTS idx_tasks_area_status;

ALTER TABLE tasks DROP COLUMN IF EXISTS area;

DROP TABLE IF EXISTS task_areas;
