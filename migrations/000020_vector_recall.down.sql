-- Revert migration 000020: vector embedding recall.
ALTER TABLE decisions DROP COLUMN IF EXISTS embedding;
