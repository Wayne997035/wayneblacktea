-- SQLite-dialect twin of migrations/000019_decay_columns.up.sql.
--
-- The SQLite backend has no incremental migration runner — schema.sql is
-- applied idempotently at sqlite.New() time. This file is a reference script
-- for operators who need to add the columns to an existing SQLite DB.
--
-- Differences vs Postgres twin:
--   * No FLOAT keyword — use REAL
--   * ALTER TABLE ADD COLUMN does not support multiple columns in one statement
--   * No CREATE OR REPLACE VIEW — Postgres view uses EXTRACT(EPOCH) which is
--     unsupported in SQLite; strength is computed app-side via ComputeStrength()
--   * No IF NOT EXISTS on ALTER TABLE ADD COLUMN in SQLite < 3.37 (modernc.org
--     ships 3.49+, so it is safe)

ALTER TABLE knowledge_items ADD COLUMN importance       REAL    NOT NULL DEFAULT 0.5;
ALTER TABLE knowledge_items ADD COLUMN recall_count     INTEGER NOT NULL DEFAULT 0;
ALTER TABLE knowledge_items ADD COLUMN last_recalled_at TEXT;
ALTER TABLE knowledge_items ADD COLUMN base_lambda      REAL    NOT NULL DEFAULT 0.1;
ALTER TABLE knowledge_items ADD COLUMN archived_at      TEXT;

ALTER TABLE concepts ADD COLUMN importance       REAL    NOT NULL DEFAULT 0.5;
ALTER TABLE concepts ADD COLUMN recall_count     INTEGER NOT NULL DEFAULT 0;
ALTER TABLE concepts ADD COLUMN last_recalled_at TEXT;
ALTER TABLE concepts ADD COLUMN base_lambda      REAL    NOT NULL DEFAULT 0.1;
ALTER TABLE concepts ADD COLUMN archived_at      TEXT;

CREATE INDEX IF NOT EXISTS idx_knowledge_archived_at
    ON knowledge_items(archived_at) WHERE archived_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_knowledge_last_recalled_at
    ON knowledge_items(last_recalled_at);

CREATE INDEX IF NOT EXISTS idx_concepts_archived_at
    ON concepts(archived_at) WHERE archived_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_concepts_last_recalled_at
    ON concepts(last_recalled_at);
