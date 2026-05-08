-- SQLite Migration 000027: add hierarchy columns to knowledge_items.
--
-- SQLite supports ADD COLUMN for nullable columns with a DEFAULT. No table
-- rebuild is required (ALTER TABLE … ADD COLUMN is an O(1) operation in
-- SQLite 3.37+). Partial indexes are supported since SQLite 3.8.9.
--
-- NOTE: The SQLite-backed runtime uses internal/storage/sqlite/schema.sql
-- directly (applied idempotently at sqlite.New()). This migration file
-- exists for Postgres↔SQLite numbering parity (backend-security-design.md §6.3)
-- and matches the changes made to schema.sql.

ALTER TABLE knowledge_items ADD COLUMN parent_id     TEXT    DEFAULT NULL;
ALTER TABLE knowledge_items ADD COLUMN heading_path  TEXT    DEFAULT NULL;
ALTER TABLE knowledge_items ADD COLUMN heading_level INTEGER DEFAULT NULL;

CREATE INDEX IF NOT EXISTS idx_knowledge_items_parent_id
    ON knowledge_items(parent_id) WHERE parent_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_knowledge_items_heading_path
    ON knowledge_items(heading_path) WHERE heading_path IS NOT NULL;
