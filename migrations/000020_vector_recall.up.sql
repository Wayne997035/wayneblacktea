-- Migration 000020: vector embedding recall (rewritten)
--
-- Adds embedding column to decisions for cosine recall.
-- Both Postgres and SQLite use BYTEA/BLOB; Go code does brute-force cosine scan.
--
-- Earlier attempt to add ivfflat indexes was removed — pgvector 0.8.1 does not
-- support `bytea::vector(32)` cast directly. To enable ivfflat in the future,
-- migrate the column type to `vector(32)` and rewrite the index in a new migration.
ALTER TABLE decisions ADD COLUMN IF NOT EXISTS embedding BYTEA;
