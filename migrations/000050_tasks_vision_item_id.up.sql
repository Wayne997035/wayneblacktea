-- Migration 000050: add vision_item_id column to tasks.
-- Enables the promote_vision_to_task flow to record which vision item a task
-- was promoted from, and allows timeline to show KindVisionPromoted events.
-- NO FOREIGN KEY (CLAUDE.md red-line §9; referential integrity in code).
-- The corresponding vision_items.promoted_task_id column was already added
-- in an earlier migration by the vision domain (non-numbered, in schema.sql).
ALTER TABLE tasks ADD COLUMN IF NOT EXISTS vision_item_id UUID;
CREATE INDEX IF NOT EXISTS idx_tasks_vision_item_id ON tasks(vision_item_id) WHERE vision_item_id IS NOT NULL;
