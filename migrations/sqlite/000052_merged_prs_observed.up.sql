-- SQLite parity for migration 000052 (merged_prs_observed).
-- See migrations/000052_merged_prs_observed.up.sql for the Postgres source
-- of truth and the rationale (Phase 2 fuzzy-match for GTD-fix 10/12; 30-day
-- retention enforced by the scheduler; NO FOREIGN KEY per CLAUDE.md §9).
--
-- Dialect differences: UUID → TEXT, TIMESTAMPTZ → TEXT (RFC3339 ms emulation
-- matches every other SQLite table in this schema).
CREATE TABLE IF NOT EXISTS merged_prs_observed (
    id            TEXT PRIMARY KEY,
    workspace_id  TEXT,
    repo          TEXT NOT NULL,
    url           TEXT NOT NULL,
    head_ref      TEXT,
    title         TEXT,
    body_excerpt  TEXT,
    merged_at     TEXT NOT NULL,
    observed_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_merged_prs_observed_url ON merged_prs_observed(url);
CREATE INDEX IF NOT EXISTS idx_merged_prs_observed_observed_at_desc ON merged_prs_observed(observed_at DESC);
CREATE INDEX IF NOT EXISTS idx_merged_prs_observed_workspace_id ON merged_prs_observed(workspace_id) WHERE workspace_id IS NOT NULL;
