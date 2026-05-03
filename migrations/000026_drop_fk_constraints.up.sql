-- Migration 000026: drop ALL foreign-key constraints (red line #9)
--
-- Project policy (CLAUDE.md #9): referential integrity MUST live in code,
-- not in DB constraints. Indexes on the reference columns are kept and
-- encouraged. The cascade behaviour previously enforced via
-- `ON DELETE CASCADE` / `ON DELETE SET NULL` is replicated in the
-- service-layer `DeleteTask` (see internal/gtd/store.go,
-- internal/storage/sqlite/gtd.go).
--
-- Postgres auto-names FKs as `<table>_<column>_fkey`. Use IF EXISTS so the
-- migration is idempotent and tolerant of historical schemas where the FK
-- might already have been dropped.

ALTER TABLE projects          DROP CONSTRAINT IF EXISTS projects_goal_id_fkey;
ALTER TABLE tasks             DROP CONSTRAINT IF EXISTS tasks_project_id_fkey;
ALTER TABLE activity_log      DROP CONSTRAINT IF EXISTS activity_log_project_id_fkey;
ALTER TABLE decisions         DROP CONSTRAINT IF EXISTS decisions_project_id_fkey;
ALTER TABLE session_handoffs  DROP CONSTRAINT IF EXISTS session_handoffs_project_id_fkey;
ALTER TABLE review_schedule   DROP CONSTRAINT IF EXISTS review_schedule_concept_id_fkey;
ALTER TABLE work_sessions     DROP CONSTRAINT IF EXISTS work_sessions_project_id_fkey;
ALTER TABLE work_sessions     DROP CONSTRAINT IF EXISTS work_sessions_current_task_id_fkey;
ALTER TABLE work_session_tasks DROP CONSTRAINT IF EXISTS work_session_tasks_session_id_fkey;
ALTER TABLE work_session_tasks DROP CONSTRAINT IF EXISTS work_session_tasks_task_id_fkey;
