-- SQLite-dialect twin of migrations/000014_project_arch.up.sql.
--
-- The SQLite backend has no incremental migration runner — schema.sql is
-- applied idempotently at sqlite.New() time. This file is a reference script
-- for operators who need to add the table to an existing SQLite DB.
--
-- Differences vs Postgres twin:
--   * id is TEXT (UUID generated app-side)
--   * JSONB → TEXT (json1 functions available since SQLite 3.9)
--   * TIMESTAMPTZ → TEXT (RFC3339 UTC; emulated via strftime)
--   * gen_random_uuid() → UUID generated in Go

CREATE TABLE IF NOT EXISTS project_arch (
    id              TEXT PRIMARY KEY,
    slug            TEXT NOT NULL UNIQUE,
    summary         TEXT NOT NULL,
    file_map        TEXT NOT NULL DEFAULT '{}',
    last_commit_sha TEXT NOT NULL DEFAULT '',
    updated_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

