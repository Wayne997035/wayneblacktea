-- Migration 000040: GIN expression index for FTS on knowledge_items.
-- to_tsvector('english', title || ' ' || content) @@ plainto_tsquery(...)
-- currently forces sequential scan. GIN enables Bitmap Index Scan.
CREATE INDEX IF NOT EXISTS idx_knowledge_items_fts_gin
    ON knowledge_items
    USING GIN (to_tsvector('english', title || ' ' || content));
