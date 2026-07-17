-- Migration 000068 rollback: drop the 3 indexes added by the .up migration
DROP INDEX IF EXISTS idx_outcomes_work_session_id;
DROP INDEX IF EXISTS idx_work_sessions_outcome_id;
DROP INDEX IF EXISTS idx_work_session_evidence_created_at;
