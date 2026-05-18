DROP INDEX IF EXISTS idx_knowledge_items_decision_id;
DROP INDEX IF EXISTS idx_knowledge_items_task_id;
DROP INDEX IF EXISTS idx_knowledge_items_project_id;
ALTER TABLE knowledge_items DROP COLUMN IF EXISTS decision_id;
ALTER TABLE knowledge_items DROP COLUMN IF EXISTS task_id;
ALTER TABLE knowledge_items DROP COLUMN IF EXISTS project_id;
