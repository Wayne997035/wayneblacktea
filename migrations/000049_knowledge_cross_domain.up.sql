-- Migration 000049: add cross-domain reference columns to knowledge_items.
-- Enables queries: "which knowledge items relate to this project/task/decision?"
-- NO FOREIGN KEY (CLAUDE.md red-line §9; referential integrity in code).
-- All columns are nullable UUIDs stored as plain columns; the application
-- layer validates UUIDs on write and uses LEFT JOIN on read.
ALTER TABLE knowledge_items ADD COLUMN IF NOT EXISTS project_id  UUID;
ALTER TABLE knowledge_items ADD COLUMN IF NOT EXISTS task_id     UUID;
ALTER TABLE knowledge_items ADD COLUMN IF NOT EXISTS decision_id UUID;

CREATE INDEX IF NOT EXISTS idx_knowledge_items_project_id  ON knowledge_items(project_id)  WHERE project_id  IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_knowledge_items_task_id     ON knowledge_items(task_id)     WHERE task_id     IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_knowledge_items_decision_id ON knowledge_items(decision_id) WHERE decision_id IS NOT NULL;
