DROP INDEX IF EXISTS idx_decisions_task_id;
ALTER TABLE decisions DROP COLUMN IF EXISTS task_id;
