-- Migration 000028 rollback: remove per-workspace composite unique constraint
-- on repos and restore the original global UNIQUE(name) from migration 000002.

ALTER TABLE repos DROP CONSTRAINT IF EXISTS repos_workspace_name_unique;

ALTER TABLE repos ADD CONSTRAINT repos_name_key UNIQUE (name);
