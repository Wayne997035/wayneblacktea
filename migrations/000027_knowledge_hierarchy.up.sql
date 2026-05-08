-- Migration 000027: add hierarchy columns to knowledge_items.
--
-- Enables tree-structured knowledge (e.g. "Introduction > Key Concepts > Embeddings")
-- for the K1 hierarchy feature. Three nullable columns; no FK constraint (CLAUDE.md #9).
-- Referential integrity for parent_id is enforced in code (service/handler layer).
--
-- Two partial indexes are added to keep scans fast for hierarchy queries while
-- avoiding index bloat on the large fraction of rows that have NULL parent_id /
-- heading_path. Retention policy: these columns are structural metadata on
-- existing rows — no TTL required (not an event / observation table).

ALTER TABLE knowledge_items ADD COLUMN IF NOT EXISTS parent_id     UUID DEFAULT NULL;
ALTER TABLE knowledge_items ADD COLUMN IF NOT EXISTS heading_path  TEXT DEFAULT NULL;
ALTER TABLE knowledge_items ADD COLUMN IF NOT EXISTS heading_level INT  DEFAULT NULL;

CREATE INDEX IF NOT EXISTS idx_knowledge_items_parent_id
    ON knowledge_items(parent_id) WHERE parent_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_knowledge_items_heading_path
    ON knowledge_items(heading_path) WHERE heading_path IS NOT NULL;
