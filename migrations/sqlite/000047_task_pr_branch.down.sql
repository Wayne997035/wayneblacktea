-- SQLite schema is managed via schema.sql applied at Open() — down migration is a no-op by design.
-- To roll back: update schema.sql to remove branch_name, pr_url, commit_shas columns and reopen the DB.
SELECT 1;
