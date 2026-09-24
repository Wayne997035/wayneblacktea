-- 000080_deletion_tombstones.down.sql (SQLite twin)
-- [F191-03]

DROP INDEX IF EXISTS idx_deletion_tombstones_deletion_id;
DROP INDEX IF EXISTS idx_deletion_tombstones_deleted_at;
DROP INDEX IF EXISTS idx_deletion_tombstones_project_deleted_at;

DROP TABLE IF EXISTS deletion_tombstones;
