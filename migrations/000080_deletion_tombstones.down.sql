-- 000080_deletion_tombstones.down.sql
-- [F191-03]
--
-- Drop order: indexes first (PostgreSQL would drop them automatically with
-- the table, but explicit DROP INDEX keeps this file symmetric with the up
-- migration and with the rest of this repo's down migrations), then the
-- table itself.

DROP INDEX IF EXISTS idx_deletion_tombstones_deletion_id;
DROP INDEX IF EXISTS idx_deletion_tombstones_deleted_at;
DROP INDEX IF EXISTS idx_deletion_tombstones_project_deleted_at;

DROP TABLE IF EXISTS deletion_tombstones;
