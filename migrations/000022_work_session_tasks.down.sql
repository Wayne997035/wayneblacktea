-- Migration 000022 rollback: drop work_session_tasks join table
DROP INDEX IF EXISTS idx_work_session_tasks_task_id;
DROP INDEX IF EXISTS idx_work_session_tasks_session_id;
DROP TABLE IF EXISTS work_session_tasks;
