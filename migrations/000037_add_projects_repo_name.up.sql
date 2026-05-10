-- Migration 000037: add `repo_name` to projects so workspace repo overview
-- can link a repo card to one or more projects without requiring
-- project.name == repo.name (which doesn't hold in practice — e.g. repo
-- "wayneblacktea" hosts project "wbt-core-mvp").
--
-- Nullable: a project may legitimately not be paired with any repo (pure
-- planning project). The handler treats NULL as "no repo link".
--
-- Index: (workspace_id, repo_name) supports the common lookup
-- "ProjectsByRepoName(workspace, repo_name)" used by the workspace
-- overview drill-down. Per backend-security-design.md §1.1 we deliberately
-- DO NOT add a FOREIGN KEY to repos(name) — referential integrity is
-- maintained by the application code (handler layer) and covered by tests.

ALTER TABLE projects ADD COLUMN IF NOT EXISTS repo_name TEXT;

CREATE INDEX IF NOT EXISTS idx_projects_workspace_repo_name
    ON projects (workspace_id, repo_name)
    WHERE repo_name IS NOT NULL;

COMMENT ON COLUMN projects.repo_name IS
    'Optional repo binding for workspace overview drill-down; populated by hand-rolled UPDATE post-migration';
