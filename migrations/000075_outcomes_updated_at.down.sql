-- 000075_outcomes_updated_at.down.sql
ALTER TABLE outcomes DROP COLUMN IF EXISTS updated_at;
