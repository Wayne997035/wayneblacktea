DROP INDEX IF EXISTS idx_projects_workspace_repo_name;
ALTER TABLE projects DROP COLUMN IF EXISTS repo_name;
