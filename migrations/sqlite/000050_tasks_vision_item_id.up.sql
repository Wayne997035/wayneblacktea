-- SQLite parity for migration 000050 (tasks.vision_item_id).
-- Column is plain TEXT (UUID as 8-4-4-4-12 canonical string).
-- No FOREIGN KEY per CLAUDE.md red-line §9.
ALTER TABLE tasks ADD COLUMN vision_item_id TEXT;
CREATE INDEX IF NOT EXISTS idx_tasks_vision_item_id ON tasks(vision_item_id);
