-- SQLite-dialect twin of migrations/000018_project_status_snapshots.up.sql.
--
-- The SQLite backend has no incremental migration runner — schema.sql is
-- applied idempotently at sqlite.New() time. This file is a reference script
-- for operators who need to add the table to an existing SQLite DB.
--
-- Differences vs Postgres twin:
--   * UUID PRIMARY KEY is TEXT (app-side generation via uuid.New().String())
--   * TIMESTAMPTZ → TEXT (RFC3339 UTC, app-side comparison)
--   * UUID[] → TEXT (JSON-encoded array, parsed app-side)
--   * BYTEA → BLOB
--   * No gen_random_uuid() — id must be supplied by the application

CREATE TABLE IF NOT EXISTS project_status_snapshots (
    id                   TEXT PRIMARY KEY,
    slug                 TEXT NOT NULL,
    workspace_id         TEXT,
    generated_at         TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    sprint_summary       TEXT,
    gap_analysis         TEXT,
    sota_catchup_pct     INTEGER,
    pending_summary      TEXT,
    source_decision_ids  TEXT NOT NULL DEFAULT '[]',
    embedding            BLOB,
    source               TEXT NOT NULL DEFAULT 'auto-status-snapshot'
);

CREATE INDEX IF NOT EXISTS idx_status_snapshots_slug_time
    ON project_status_snapshots (slug, generated_at DESC);
