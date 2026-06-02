-- 000062_memory_atoms_promoted_status.up.sql (SQLite twin)
-- SQLite does not support COMMENT ON COLUMN, so this migration is a no-op.
-- The 'promoted' digest_status value is handled application-side by the
-- atom-bridge scheduler job (internal/scheduler/atom_bridge.go).
SELECT 1;
