-- SQLite Migration 000028 rollback: remove per-workspace composite unique index
-- and restore a global unique index on repos.name.
--
-- SQLite cannot add inline UNIQUE constraints via ALTER TABLE, so the rollback
-- creates an equivalent unique index instead of a column-level constraint.

DROP INDEX IF EXISTS idx_repos_workspace_name_unique;

CREATE UNIQUE INDEX IF NOT EXISTS idx_repos_name_unique ON repos (name);
