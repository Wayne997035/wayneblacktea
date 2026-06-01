-- M9: document 'consolidated' as a valid digest_status value.
-- The four valid values are: pending, done, failed, consolidated.
-- No schema change needed — digest_status is an unconstrained TEXT column
-- (migration 000053); constraint enforcement is done at the application layer.
COMMENT ON COLUMN memory_atoms.digest_status IS 'pending | done | failed | consolidated';
