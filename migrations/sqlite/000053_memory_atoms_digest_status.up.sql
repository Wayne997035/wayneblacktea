ALTER TABLE memory_atoms ADD COLUMN digest_status TEXT NOT NULL DEFAULT 'pending';
ALTER TABLE memory_atoms ADD COLUMN error_msg TEXT;
