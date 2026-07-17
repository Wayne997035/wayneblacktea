-- Migration 000065 rollback: drop work_sessions evidence + verification columns
ALTER TABLE work_sessions DROP COLUMN IF EXISTS context_pack_id;
ALTER TABLE work_sessions DROP COLUMN IF EXISTS verification_status;
ALTER TABLE work_sessions DROP COLUMN IF EXISTS verification_command;
ALTER TABLE work_sessions DROP COLUMN IF EXISTS verification_output_excerpt;
ALTER TABLE work_sessions DROP COLUMN IF EXISTS outcome_id;
ALTER TABLE work_sessions DROP COLUMN IF EXISTS final_result;
ALTER TABLE work_sessions DROP COLUMN IF EXISTS branch_name;
