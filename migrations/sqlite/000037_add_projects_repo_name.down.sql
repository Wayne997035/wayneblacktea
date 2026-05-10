DROP INDEX IF EXISTS idx_projects_workspace_repo_name;
-- SQLite supports DROP COLUMN since 3.35 (2021); fall back to a recreate
-- if you're on an older binary. wayneblacktea pins modern SQLite via
-- mattn/go-sqlite3, so this is safe.
ALTER TABLE projects DROP COLUMN repo_name;
