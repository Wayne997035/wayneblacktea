-- Migration 000020: vector embedding recall
--
-- 1. ADD decisions.embedding BYTEA (nullable, filled async by Stop hook future work).
-- 2. ADD ivfflat index on session_handoffs.embedding for cosine distance search.
--    pgvector v0.5+ supports vector_cosine_ops on BYTEA-backed columns once
--    cast to vector.  Since session_handoffs.embedding is BYTEA (added in 000017)
--    we need a functional index: CREATE INDEX ... ON ... USING ivfflat ((embedding::vector(32))).
--    NOTE: ivfflat requires at least 'lists' rows already in the table to build.
--    We use CREATE INDEX IF NOT EXISTS with lists=1 which is safe for small tables.
--
-- Postgres-only: SQLite has no pgvector; SearchByCosine falls back to brute-force
-- Go-side cosine scan (table is small — acceptable for personal-OS scale).

-- 1. Add embedding column to decisions (workspace-scoped cosine recall).
ALTER TABLE decisions
    ADD COLUMN IF NOT EXISTS embedding BYTEA;

-- 2. Functional ivfflat index on session_handoffs.embedding.
--    Requires: CREATE EXTENSION IF NOT EXISTS vector (already done in earlier migration).
--    lists=1 is correct for very small tables (personal-OS scale).
--    G115 (gosec): nolint not needed here — pure SQL DDL.
CREATE INDEX IF NOT EXISTS idx_session_handoffs_embedding_cosine
    ON session_handoffs
    USING ivfflat ((embedding::vector(32)) vector_cosine_ops)
    WITH (lists = 1);

-- 3. Functional ivfflat index on decisions.embedding.
CREATE INDEX IF NOT EXISTS idx_decisions_embedding_cosine
    ON decisions
    USING ivfflat ((embedding::vector(32)) vector_cosine_ops)
    WITH (lists = 1);
