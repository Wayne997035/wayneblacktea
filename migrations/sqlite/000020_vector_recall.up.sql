-- SQLite-dialect twin of migrations/000020_vector_recall.up.sql.
--
-- Differences vs Postgres twin:
--   * BYTEA → BLOB
--   * No pgvector extension → no ivfflat index (brute-force Go-side scan used instead)
--   * SQLite ALTER TABLE only supports ADD COLUMN — no IF NOT EXISTS before SQLite 3.37
--     (modernc.org/sqlite ships 3.49+ so this is safe)

ALTER TABLE decisions ADD COLUMN embedding BLOB;
