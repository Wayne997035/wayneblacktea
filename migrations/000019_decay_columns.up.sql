-- Migration 000019: Ebbinghaus decay fields for knowledge_items and concepts.
-- Adds importance, recall_count, last_recalled_at, base_lambda, archived_at columns.
-- Postgres-dialect only; SQLite twin is in migrations/sqlite/000019_*.
--
-- Decision NOT to alter decisions table (it is an audit trail / truth source).

ALTER TABLE knowledge_items
    ADD COLUMN IF NOT EXISTS importance       FLOAT         NOT NULL DEFAULT 0.5,
    ADD COLUMN IF NOT EXISTS recall_count     INTEGER       NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS last_recalled_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS base_lambda      FLOAT         NOT NULL DEFAULT 0.1,
    ADD COLUMN IF NOT EXISTS archived_at      TIMESTAMPTZ;

ALTER TABLE concepts
    ADD COLUMN IF NOT EXISTS importance       FLOAT         NOT NULL DEFAULT 0.5,
    ADD COLUMN IF NOT EXISTS recall_count     INTEGER       NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS last_recalled_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS base_lambda      FLOAT         NOT NULL DEFAULT 0.1,
    ADD COLUMN IF NOT EXISTS archived_at      TIMESTAMPTZ;

-- Indexes to make soft-delete pruner and strength queries efficient.
CREATE INDEX IF NOT EXISTS idx_knowledge_archived_at
    ON knowledge_items(archived_at) WHERE archived_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_knowledge_last_recalled_at
    ON knowledge_items(last_recalled_at);

CREATE INDEX IF NOT EXISTS idx_concepts_archived_at
    ON concepts(archived_at) WHERE archived_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_concepts_last_recalled_at
    ON concepts(last_recalled_at);

-- View: knowledge_ranked orders by Ebbinghaus strength so callers get
-- decay-aware recall without repeating the formula in every query.
--
-- Formula:
--   age_days = EXTRACT(EPOCH FROM (NOW() - COALESCE(last_recalled_at, created_at))) / 86400
--   strength = CLAMP(importance * EXP(-base_lambda * (1 - importance*0.8) * age_days) * (1 + recall_count * 0.2), 0, 1)
CREATE OR REPLACE VIEW knowledge_ranked AS
SELECT
    ki.*,
    GREATEST(0.0, LEAST(1.0,
        ki.importance
        * EXP(
            -ki.base_lambda
            * (1.0 - ki.importance * 0.8)
            * EXTRACT(EPOCH FROM (NOW() - COALESCE(ki.last_recalled_at, ki.created_at))) / 86400.0
          )
        * (1.0 + ki.recall_count * 0.2)
    )) AS strength
FROM knowledge_items ki
WHERE ki.archived_at IS NULL;
