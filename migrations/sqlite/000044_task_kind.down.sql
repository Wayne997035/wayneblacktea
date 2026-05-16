-- SQLite does not support DROP COLUMN in older versions; recreate table is complex.
-- For rollback, accept manual intervention or leave as SELECT 1 no-op.
SELECT 1; -- down migration: kind column remains in place (general default, no data loss)
