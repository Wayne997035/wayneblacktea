-- Migration 000027 rollback: remove hierarchy indexes and columns.
--
-- Plain SQL only; no psql metacommands (\set, :variable, \copy, etc.) —
-- golang-migrate cannot parse them (backend-security-design.md §6.1).

DROP INDEX IF EXISTS idx_knowledge_items_heading_path;
DROP INDEX IF EXISTS idx_knowledge_items_parent_id;
ALTER TABLE knowledge_items DROP COLUMN IF EXISTS heading_level;
ALTER TABLE knowledge_items DROP COLUMN IF EXISTS heading_path;
ALTER TABLE knowledge_items DROP COLUMN IF EXISTS parent_id;
