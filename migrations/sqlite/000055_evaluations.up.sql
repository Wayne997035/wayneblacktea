-- SQLite parity for migration 000055 (evaluations).
-- See migrations/000055_evaluations.up.sql for the Postgres source of truth.
--
-- Dialect differences: UUID → TEXT, TIMESTAMPTZ → TEXT (RFC3339),
-- JSONB → TEXT. No FOREIGN KEY per CLAUDE.md §9.
CREATE TABLE IF NOT EXISTS evaluations (
    id                      TEXT PRIMARY KEY,
    workspace_id            TEXT,
    outcome_id              TEXT NOT NULL,
    analysis                TEXT NOT NULL,
    lessons                 TEXT NOT NULL DEFAULT '[]',
    improvement_suggestions TEXT NOT NULL DEFAULT '[]',
    created_at              TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

CREATE INDEX IF NOT EXISTS idx_evaluations_outcome_id ON evaluations(outcome_id);
CREATE INDEX IF NOT EXISTS idx_evaluations_workspace_id ON evaluations(workspace_id) WHERE workspace_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_evaluations_created_at ON evaluations(created_at DESC);
