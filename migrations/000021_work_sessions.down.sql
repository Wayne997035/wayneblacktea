-- Migration 000021 rollback: drop work_sessions table
DROP INDEX IF EXISTS idx_work_sessions_status;
DROP INDEX IF EXISTS idx_work_sessions_repo_name;
DROP INDEX IF EXISTS idx_work_sessions_workspace_id;
DROP INDEX IF EXISTS idx_work_sessions_one_active;
DROP TABLE IF EXISTS work_sessions;
