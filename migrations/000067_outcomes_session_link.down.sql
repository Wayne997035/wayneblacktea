-- 000067_outcomes_session_link.down.sql
ALTER TABLE outcomes DROP COLUMN IF EXISTS work_session_id;
