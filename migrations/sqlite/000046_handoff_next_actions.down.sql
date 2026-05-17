-- SQLite schema is managed via schema.sql applied at Open() — down migration is a no-op by design.
-- To roll back: update schema.sql to remove the next_actions column and reopen the DB.
SELECT 1;
