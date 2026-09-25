-- 000083_repos_github_slug.up.sql (SQLite twin)
--
-- See migrations/000083_repos_github_slug.up.sql for the rationale. SQLite's
-- ALTER TABLE ADD COLUMN has no IF NOT EXISTS form, so a re-run fails loudly
-- (same note as 000076's SQLite twin).
ALTER TABLE repos ADD COLUMN github_slug TEXT;
