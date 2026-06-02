-- 000062_memory_atoms_promoted_status.down.sql
-- Restore the digest_status column comment to the 000060 description.
COMMENT ON COLUMN memory_atoms.digest_status IS
    'pending | done | failed | consolidated';
