-- SQLite Migration 000021 rollback
DROP INDEX IF EXISTS idx_work_sessions_status;
DROP INDEX IF EXISTS idx_work_sessions_repo_name;
DROP INDEX IF EXISTS idx_work_sessions_workspace_id;
DROP INDEX IF EXISTS idx_work_sessions_one_active;
DROP TABLE IF EXISTS work_sessions;
