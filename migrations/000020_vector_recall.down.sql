-- Revert migration 000020: vector embedding recall.
-- Drop indexes first, then the column.

DROP INDEX IF EXISTS idx_decisions_embedding_cosine;
DROP INDEX IF EXISTS idx_session_handoffs_embedding_cosine;

ALTER TABLE decisions
    DROP COLUMN IF EXISTS embedding;
