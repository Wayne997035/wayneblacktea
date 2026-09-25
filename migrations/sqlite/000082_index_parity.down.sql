-- 000082_index_parity.down.sql
-- [F0925-15]
--
-- Reverts the 3 indexes to their pre-000082 SQLite shape (the drift this
-- migration fixes), each as DROP INDEX + CREATE INDEX to keep the file
-- symmetric with the up migration.

DROP INDEX IF EXISTS idx_work_sessions_repo_name;
CREATE INDEX IF NOT EXISTS idx_work_sessions_repo_name
    ON work_sessions(workspace_id, repo_name, created_at);

DROP INDEX IF EXISTS idx_work_sessions_workspace_id;
CREATE INDEX IF NOT EXISTS idx_work_sessions_workspace_id
    ON work_sessions(workspace_id)
    WHERE workspace_id IS NOT NULL;

DROP INDEX IF EXISTS idx_decisions_task_id;
CREATE INDEX IF NOT EXISTS idx_decisions_task_id
    ON decisions(task_id);
