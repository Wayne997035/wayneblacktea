-- SQLite parity for migration 000054 (outcomes).
-- See migrations/000054_outcomes.up.sql for the Postgres source of truth.
--
-- Dialect differences: UUID → TEXT, TIMESTAMPTZ → TEXT (RFC3339),
-- JSONB → TEXT (json1 functions available since SQLite 3.9).
-- No FOREIGN KEY per CLAUDE.md §9.
CREATE TABLE IF NOT EXISTS outcomes (
    id           TEXT PRIMARY KEY,
    workspace_id TEXT,
    entity_type  TEXT NOT NULL,
    entity_id    TEXT NOT NULL,
    result       TEXT NOT NULL CHECK (result IN ('success','failure','partial','unknown','regressed')),
    metrics      TEXT,
    notes        TEXT,
    created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

CREATE INDEX IF NOT EXISTS idx_outcomes_workspace_entity ON outcomes(workspace_id, entity_type, entity_id);
CREATE INDEX IF NOT EXISTS idx_outcomes_entity_id ON outcomes(entity_id);
CREATE INDEX IF NOT EXISTS idx_outcomes_result ON outcomes(result);
CREATE INDEX IF NOT EXISTS idx_outcomes_created_at ON outcomes(created_at DESC);
