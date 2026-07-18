-- SQLite Migration 000028: repos name → per-workspace composite unique.
--
-- SQLite does not support ALTER TABLE ... DROP CONSTRAINT / ADD CONSTRAINT for
-- unique constraints. The schema.sql file (applied idempotently at sqlite.Open)
-- has already been updated to declare the per-workspace composite unique
-- via the index below (replacing the former `name TEXT NOT NULL UNIQUE` column
-- definition).
--
-- For dev/local SQLite instances that already have the old schema, we:
--   1. Drop the old auto-created unique index on name.
--   2. Create the new composite unique index on (workspace_id, name).
--
-- This migration is idempotent: IF NOT EXISTS / IF EXISTS guards are used.
-- The SQLite runtime uses schema.sql (not these numbered migrations) for fresh
-- databases, so this file exists only for Postgres↔SQLite numbering parity
-- (backend-security-design.md §6.3) and for upgrading pre-existing dev DBs.

DROP INDEX IF EXISTS idx_repos_name_unique;

-- COALESCE(workspace_id,'') is required because SQLite treats NULL != NULL,
-- so a plain (workspace_id, name) index would not enforce uniqueness for rows
-- where workspace_id IS NULL (legacy single-tenant mode).
CREATE UNIQUE INDEX IF NOT EXISTS idx_repos_workspace_name_unique
    ON repos (COALESCE(workspace_id,''), name);
