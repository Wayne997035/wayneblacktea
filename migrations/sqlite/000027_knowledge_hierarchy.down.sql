-- SQLite Migration 000027 rollback: remove hierarchy indexes.
--
-- SQLite (before version 3.35.0, 2021-03-12) does not support
-- ALTER TABLE ... DROP COLUMN. Rather than a full table rebuild for a
-- down migration that is unlikely to be applied in production, we drop
-- only the indexes here. The columns remain but are harmless nullable
-- extras. If a full column removal is required on a SQLite 3.35+ instance,
-- perform the ALTER TABLE ... DROP COLUMN manually after this migration runs.

DROP INDEX IF EXISTS idx_knowledge_items_heading_path;
DROP INDEX IF EXISTS idx_knowledge_items_parent_id;
