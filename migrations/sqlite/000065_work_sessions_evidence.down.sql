-- 000065_work_sessions_evidence.down.sql (SQLite twin)
-- SQLite 3.35+ supports DROP COLUMN. Down migrations are only applied via
-- the migrate-down Taskfile target; applyColumnUpgrades() does not run this.
ALTER TABLE work_sessions DROP COLUMN context_pack_id;
ALTER TABLE work_sessions DROP COLUMN verification_status;
ALTER TABLE work_sessions DROP COLUMN verification_command;
ALTER TABLE work_sessions DROP COLUMN verification_output_excerpt;
ALTER TABLE work_sessions DROP COLUMN outcome_id;
ALTER TABLE work_sessions DROP COLUMN final_result;
ALTER TABLE work_sessions DROP COLUMN branch_name;
