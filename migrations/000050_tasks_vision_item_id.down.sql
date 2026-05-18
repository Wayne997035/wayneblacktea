DROP INDEX IF EXISTS idx_tasks_vision_item_id;
ALTER TABLE tasks DROP COLUMN IF EXISTS vision_item_id;
