-- 000064_embedding_provider_marker.up.sql
--
-- Adds embedding provider metadata columns to the three tables that have a
-- BYTEA embedding column: session_handoffs, decisions, project_status_snapshots.
--
-- Design notes:
--   * All three columns are nullable with no DEFAULT; existing rows keep NULL.
--   * embedding_provider is a short text tag: 'gemini', 'hashed', or 'unknown'.
--   * embedding_model is e.g. 'gemini-embedding-001' for Gemini rows.
--   * embedding_dim is the vector length (32 for hashed, 768 for Gemini-001).
--   * No FOREIGN KEY per CLAUDE.md red-line #9; referential integrity in code.
--   * No index in Phase 1: SearchByCosine already does a full scan of at most
--     200 recent rows; an index on embedding_provider is premature at this scale.
--   * Retention: follows the row's table TTL (no separate TTL).
--
-- After adding columns, tag all existing non-null-embedding rows as 'hashed'/32
-- because the only writer before this sprint was HashedEmbeddingProvider (32-dim).
-- knowledge_items.embedding is intentionally excluded: it uses the real Gemini
-- 768-dim provider already (factory.go:189) and has no embedding_provider column
-- to update (a future migration can add it there if needed).

ALTER TABLE session_handoffs      ADD COLUMN IF NOT EXISTS embedding_provider TEXT;
ALTER TABLE session_handoffs      ADD COLUMN IF NOT EXISTS embedding_model     TEXT;
ALTER TABLE session_handoffs      ADD COLUMN IF NOT EXISTS embedding_dim       INT;

ALTER TABLE decisions             ADD COLUMN IF NOT EXISTS embedding_provider TEXT;
ALTER TABLE decisions             ADD COLUMN IF NOT EXISTS embedding_model     TEXT;
ALTER TABLE decisions             ADD COLUMN IF NOT EXISTS embedding_dim       INT;

ALTER TABLE project_status_snapshots ADD COLUMN IF NOT EXISTS embedding_provider TEXT;
ALTER TABLE project_status_snapshots ADD COLUMN IF NOT EXISTS embedding_model     TEXT;
ALTER TABLE project_status_snapshots ADD COLUMN IF NOT EXISTS embedding_dim       INT;

-- Tag all existing non-null-embedding rows as written by the hashed provider
-- (the only writer before this sprint was HashedEmbeddingProvider, 32-dim).
UPDATE session_handoffs
   SET embedding_provider = 'hashed',
       embedding_dim      = 32
 WHERE embedding IS NOT NULL
   AND embedding_provider IS NULL;

UPDATE decisions
   SET embedding_provider = 'hashed',
       embedding_dim      = 32
 WHERE embedding IS NOT NULL
   AND embedding_provider IS NULL;

UPDATE project_status_snapshots
   SET embedding_provider = 'hashed',
       embedding_dim      = 32
 WHERE embedding IS NOT NULL
   AND embedding_provider IS NULL;
