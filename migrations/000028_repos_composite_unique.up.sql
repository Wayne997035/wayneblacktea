-- Migration 000028: replace global UNIQUE(name) on repos with per-workspace
-- composite UNIQUE(workspace_id, name).
--
-- Background: migration 000002 created `repos.name TEXT NOT NULL UNIQUE`, which
-- is a global constraint. Two different workspaces therefore cannot each have a
-- repo with the same name. This migration corrects that by dropping the global
-- constraint and replacing it with a per-workspace composite unique index.
--
-- Both conventional auto-naming conventions are covered by the two DROP
-- CONSTRAINT IF EXISTS statements below.

ALTER TABLE repos DROP CONSTRAINT IF EXISTS repos_name_key;
ALTER TABLE repos DROP CONSTRAINT IF EXISTS unique_repos_name;

ALTER TABLE repos ADD CONSTRAINT repos_workspace_name_unique UNIQUE (workspace_id, name);
