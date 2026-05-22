CREATE TABLE reflections (
    id                   UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id         UUID,
    type                 TEXT        NOT NULL
                            CHECK (type IN ('daily','weekly','task','decision',
                                            'proposal','knowledge','system')),
    related_entity_type  TEXT,
    related_entity_id    UUID,
    summary              TEXT        NOT NULL DEFAULT '',
    insights             JSONB,
    patterns_detected    JSONB,
    suggested_actions    JSONB,
    confidence           FLOAT8      NOT NULL DEFAULT 0.0
                            CHECK (confidence BETWEEN 0.0 AND 1.0),
    created_at           TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_reflections_workspace_id     ON reflections(workspace_id) WHERE workspace_id IS NOT NULL;
CREATE INDEX idx_reflections_type_created_at  ON reflections(type, created_at DESC);
CREATE INDEX idx_reflections_related_entity   ON reflections(related_entity_type, related_entity_id) WHERE related_entity_id IS NOT NULL;
CREATE INDEX idx_reflections_created_at       ON reflections(created_at DESC);

COMMENT ON TABLE reflections IS 'Persisted AI reflection records; no automatic TTL.';
