ALTER TABLE memory_atoms
    ADD COLUMN IF NOT EXISTS digest_status TEXT NOT NULL DEFAULT 'pending',
    ADD COLUMN IF NOT EXISTS error_msg     TEXT;

COMMENT ON COLUMN memory_atoms.digest_status IS 'pending | done | failed';
CREATE INDEX IF NOT EXISTS idx_memory_atoms_digest_status ON memory_atoms(digest_status, workspace_id);
