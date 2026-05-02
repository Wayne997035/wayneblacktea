-- SQLite revert: drop decay indexes only.
-- SQLite does not support DROP COLUMN before 3.35 / modernc 3.49+ supports it,
-- but removing columns from a core table is risky without data migration tooling.
-- The safe revert path for operators is to recreate the DB from a backup.
-- Dropping the indexes is safe and reversible.

DROP INDEX IF EXISTS idx_knowledge_archived_at;
DROP INDEX IF EXISTS idx_knowledge_last_recalled_at;
DROP INDEX IF EXISTS idx_concepts_archived_at;
DROP INDEX IF EXISTS idx_concepts_last_recalled_at;
