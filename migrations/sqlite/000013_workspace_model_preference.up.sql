-- SQLite-dialect twin of migrations/000013_workspace_model_preference.up.sql.
--
-- The SQLite backend has no incremental migration runner — schema.sql is
-- applied idempotently at sqlite.New() time, so this file's CREATE TABLE
-- statement is duplicated in internal/storage/sqlite/schema.sql and gets
-- applied on every Open(). This file exists so SQLite self-hosters can
-- inspect / replay individual upgrades, mirroring the Postgres migration
-- numbering exactly.
--
-- Differences vs Postgres twin:
--   * workspace_id is TEXT (UUID rendered as canonical 8-4-4-4-12).
--   * Timestamps are TEXT (ISO-8601 UTC, default uses strftime).

CREATE TABLE IF NOT EXISTS workspace_preferences (
    workspace_id     TEXT PRIMARY KEY,
    model_preference TEXT NOT NULL DEFAULT 'claude-sonnet-4-6',
    created_at       TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    updated_at       TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
