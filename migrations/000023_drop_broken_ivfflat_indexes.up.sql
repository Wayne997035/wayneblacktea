-- Defensive cleanup: remove ivfflat indexes that were attempted by the original
-- 000020_vector_recall.up.sql before it was rewritten. The bytea::vector(32) cast
-- approach is incompatible with pgvector 0.8.1+. If those indexes were ever
-- successfully created (unlikely given the cast bug), this removes them safely.
-- Future vector recall optimizations should use a `vector` column type.
DROP INDEX IF EXISTS idx_session_handoffs_embedding_cosine;
DROP INDEX IF EXISTS idx_decisions_embedding_cosine;
