-- Revert migration 000019: drop decay columns and the knowledge_ranked view.

DROP VIEW IF EXISTS knowledge_ranked;

DROP INDEX IF EXISTS idx_knowledge_archived_at;
DROP INDEX IF EXISTS idx_knowledge_last_recalled_at;
DROP INDEX IF EXISTS idx_concepts_archived_at;
DROP INDEX IF EXISTS idx_concepts_last_recalled_at;

ALTER TABLE knowledge_items
    DROP COLUMN IF EXISTS importance,
    DROP COLUMN IF EXISTS recall_count,
    DROP COLUMN IF EXISTS last_recalled_at,
    DROP COLUMN IF EXISTS base_lambda,
    DROP COLUMN IF EXISTS archived_at;

ALTER TABLE concepts
    DROP COLUMN IF EXISTS importance,
    DROP COLUMN IF EXISTS recall_count,
    DROP COLUMN IF EXISTS last_recalled_at,
    DROP COLUMN IF EXISTS base_lambda,
    DROP COLUMN IF EXISTS archived_at;
