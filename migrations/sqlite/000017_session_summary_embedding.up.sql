-- SQLite-dialect twin of migrations/000015_session_summary_embedding.up.sql.
--
-- The SQLite backend has no incremental migration runner — schema.sql is
-- applied idempotently at sqlite.New() time. This file is a reference script
-- for operators who need to add the columns to an existing SQLite DB.
--
-- Differences vs Postgres twin:
--   * ALTER TABLE ADD COLUMN does not support IF NOT EXISTS in SQLite < 3.37
--     (modernc.org/sqlite ships 3.49+ so this is safe; kept for clarity)
--   * BYTEA → BLOB
--   * No COLUMN keyword required for ADD

ALTER TABLE session_handoffs ADD COLUMN summary_text TEXT;
ALTER TABLE session_handoffs ADD COLUMN embedding    BLOB;
