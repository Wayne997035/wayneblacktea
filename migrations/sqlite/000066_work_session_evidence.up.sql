-- 000066_work_session_evidence.up.sql (SQLite twin)
-- Mirrors migrations/000066_work_session_evidence.up.sql (Postgres).
-- Columns are plain TEXT (UUID as 8-4-4-4-12 canonical string).
-- No FOREIGN KEY per CLAUDE.md red-line #9.
CREATE TABLE IF NOT EXISTS work_session_evidence (
    id             TEXT PRIMARY KEY,
    workspace_id   TEXT,
    session_id     TEXT NOT NULL,
    evidence_type  TEXT NOT NULL CHECK (evidence_type IN ('command','pr','ci','railway','manual_note')),
    status         TEXT NOT NULL CHECK (status IN ('passed','failed','unknown')),
    command        TEXT,
    artifact       TEXT,
    output_excerpt TEXT,
    created_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

CREATE INDEX IF NOT EXISTS idx_work_session_evidence_session_id
    ON work_session_evidence(session_id);

CREATE INDEX IF NOT EXISTS idx_work_session_evidence_workspace_session_created
    ON work_session_evidence(workspace_id, session_id, created_at DESC);
