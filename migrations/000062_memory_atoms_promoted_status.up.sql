-- 000062_memory_atoms_promoted_status.up.sql
-- Documentation-only migration: clarifies the allowed values of
-- memory_atoms.digest_status which is an unconstrained TEXT column added
-- in migration 000053. The new 'promoted' value is written by the
-- atom-bridge weekly scheduler job (internal/scheduler/atom_bridge.go)
-- when a consolidated atom is promoted to a pending knowledge proposal.
-- No CHECK constraint is added because the column was intentionally left
-- unconstrained to allow forward-compatible status values.
COMMENT ON COLUMN memory_atoms.digest_status IS
    'pending | done | failed | consolidated | promoted';
