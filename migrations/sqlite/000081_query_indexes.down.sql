-- 000081_query_indexes.down.sql (SQLite twin)
-- [F0925-11]
--
-- Reverse order of the up migration, symmetric with the rest of this repo's
-- down migrations.

DROP INDEX IF EXISTS idx_memory_atoms_created_at;
DROP INDEX IF EXISTS idx_procedural_memories_project_id;
DROP INDEX IF EXISTS idx_vision_items_promoted_task_id;
DROP INDEX IF EXISTS idx_vision_items_project_id;
DROP INDEX IF EXISTS idx_vision_items_workspace_created_at;
DROP INDEX IF EXISTS idx_work_sessions_current_task_id;
DROP INDEX IF EXISTS idx_work_sessions_project_id;
DROP INDEX IF EXISTS idx_session_handoffs_project_id;
