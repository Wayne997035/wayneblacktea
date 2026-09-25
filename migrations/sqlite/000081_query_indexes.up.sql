-- 000081_query_indexes.up.sql (SQLite twin)
-- [F0925-11] SQLite twin of migrations/000081_query_indexes.up.sql. Carries
-- 8 of the 10 Postgres indexes:
--   - skips #2 (project_status_snapshots): no SQLite store reads this table
--     at all (internal/snapshot only has a Postgres store,
--     internal/snapshot/store.go, and no SQLite store queries it) — per
--     decision e0bfb453 ("add only where a real query path uses the
--     column"), no query means no index here.
--   - skips #9 (guard_bypasses): that table has no SQLite twin at all (no
--     CREATE TABLE guard_bypasses anywhere under migrations/sqlite/).
-- See the Postgres migration's per-index comments for the query path each
-- of the 8 below serves.
--
-- IF NOT EXISTS: safe to rerun. No FK constraints (red line #9).

CREATE INDEX IF NOT EXISTS idx_session_handoffs_project_id
    ON session_handoffs(project_id)
    WHERE project_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_work_sessions_project_id
    ON work_sessions(project_id)
    WHERE project_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_work_sessions_current_task_id
    ON work_sessions(current_task_id)
    WHERE current_task_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_vision_items_workspace_created_at
    ON vision_items(workspace_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_vision_items_project_id
    ON vision_items(project_id)
    WHERE project_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_vision_items_promoted_task_id
    ON vision_items(promoted_task_id)
    WHERE promoted_task_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_procedural_memories_project_id
    ON procedural_memories(project_id)
    WHERE project_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_memory_atoms_created_at
    ON memory_atoms(created_at DESC);
