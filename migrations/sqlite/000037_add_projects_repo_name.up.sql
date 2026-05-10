-- SQLite parity for migration 000037: add `repo_name` column + index.
-- Schema in internal/storage/sqlite/schema.sql declares the column directly
-- on the projects table; this migration covers existing local DBs that
-- were created before schema.sql was updated.
ALTER TABLE projects ADD COLUMN repo_name TEXT;
CREATE INDEX IF NOT EXISTS idx_projects_workspace_repo_name
    ON projects(workspace_id, repo_name)
    WHERE repo_name IS NOT NULL;
