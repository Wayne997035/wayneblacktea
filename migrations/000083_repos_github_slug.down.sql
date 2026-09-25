-- 000083_repos_github_slug.down.sql — drops the stored slugs; re-running
-- seed (or sync_repo with github_slug) repopulates them.
ALTER TABLE repos DROP COLUMN IF EXISTS github_slug;
