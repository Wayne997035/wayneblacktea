DROP INDEX IF EXISTS idx_memory_atoms_digest_status;
ALTER TABLE memory_atoms DROP COLUMN IF EXISTS error_msg;
ALTER TABLE memory_atoms DROP COLUMN IF EXISTS digest_status;
