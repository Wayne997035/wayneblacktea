-- SQLite parity for migration 000049 (knowledge_items cross-domain columns).
-- Columns are plain TEXT (UUID as 8-4-4-4-12 canonical string).
-- No FOREIGN KEY per CLAUDE.md red-line §9.
-- Partial indexes (WHERE col IS NOT NULL) are supported in SQLite 3.8.9+.
ALTER TABLE knowledge_items ADD COLUMN project_id  TEXT;
ALTER TABLE knowledge_items ADD COLUMN task_id     TEXT;
ALTER TABLE knowledge_items ADD COLUMN decision_id TEXT;
CREATE INDEX IF NOT EXISTS idx_knowledge_items_project_id  ON knowledge_items(project_id);
CREATE INDEX IF NOT EXISTS idx_knowledge_items_task_id     ON knowledge_items(task_id);
CREATE INDEX IF NOT EXISTS idx_knowledge_items_decision_id ON knowledge_items(decision_id);
