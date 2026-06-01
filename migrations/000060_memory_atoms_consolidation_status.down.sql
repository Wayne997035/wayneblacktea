-- Revert to the original three-value comment.
COMMENT ON COLUMN memory_atoms.digest_status IS 'pending | done | failed';
