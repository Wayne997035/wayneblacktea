-- SQLite Migration 000022 rollback
DROP INDEX IF EXISTS idx_work_session_tasks_task_id;
DROP INDEX IF EXISTS idx_work_session_tasks_session_id;
DROP TABLE IF EXISTS work_session_tasks;
