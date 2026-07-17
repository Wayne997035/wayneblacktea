-- 000068_work_session_evidence_indexes.up.sql (SQLite twin)
-- Mirrors migrations/000068_work_session_evidence_indexes.up.sql (Postgres).
-- SQLite supports partial indexes (3.8.0+); syntax matches the Postgres twin
-- and the existing idx_work_sessions_workspace_id / idx_outcomes_workspace_entity
-- partial indexes already in schema.sql.
-- No FOREIGN KEY per CLAUDE.md red-line #9.
CREATE INDEX IF NOT EXISTS idx_work_session_evidence_created_at
    ON work_session_evidence(created_at);

CREATE INDEX IF NOT EXISTS idx_work_sessions_outcome_id
    ON work_sessions(outcome_id) WHERE outcome_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_outcomes_work_session_id
    ON outcomes(work_session_id) WHERE work_session_id IS NOT NULL;
