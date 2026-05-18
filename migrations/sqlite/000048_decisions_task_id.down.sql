-- SQLite does not support DROP COLUMN on older versions; this migration
-- is a no-op down for SQLite. The schema.sql baseline does not have task_id,
-- so a fresh SQLite database will not have the column if created from scratch.
-- For existing SQLite databases that ran the up migration, the column remains
-- but is treated as unused — this matches SQLite's limited ALTER TABLE support.
SELECT 1;
