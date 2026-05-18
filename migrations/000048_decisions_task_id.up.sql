-- Migration 000048: add task_id column to decisions.
-- Enables cross-domain reference: "which decisions relate to this task?"
-- NO FOREIGN KEY (CLAUDE.md red-line §9; referential integrity in code).
-- task_id is a nullable UUID stored as a plain column; the application layer
-- performs any existence-check on write, and callers use LEFT JOIN on read.
ALTER TABLE decisions ADD COLUMN IF NOT EXISTS task_id UUID;
CREATE INDEX IF NOT EXISTS idx_decisions_task_id ON decisions(task_id) WHERE task_id IS NOT NULL;
