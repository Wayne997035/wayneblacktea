-- 000083_repos_github_slug.down.sql (SQLite twin). SQLite 3.35+ supports
-- DROP COLUMN (see 000076's SQLite down twin).
ALTER TABLE repos DROP COLUMN github_slug;
