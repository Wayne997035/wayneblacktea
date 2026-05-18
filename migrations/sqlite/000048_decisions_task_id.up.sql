-- SQLite parity for migration 000048 (decisions.task_id).
-- SQLite does not support ADD COLUMN IF NOT EXISTS before v3.35; use a
-- best-effort pragma check pattern for maximum compatibility.
-- The column is a plain TEXT (UUID stored as 8-4-4-4-12 canonical string),
-- no FOREIGN KEY per CLAUDE.md red-line §9.
ALTER TABLE decisions ADD COLUMN task_id TEXT;
CREATE INDEX IF NOT EXISTS idx_decisions_task_id ON decisions(task_id);
