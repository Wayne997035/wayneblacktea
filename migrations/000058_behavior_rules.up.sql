CREATE TABLE behavior_rules (
    id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id  UUID,
    condition     TEXT        NOT NULL,
    action        TEXT        NOT NULL,
    source_type   TEXT        NOT NULL
                                CHECK (source_type IN ('reflection','outcome','manual')),
    source_id     UUID,
    confidence    NUMERIC(5,2) NOT NULL DEFAULT 0.50
                                CHECK (confidence BETWEEN 0.00 AND 1.00),
    status        TEXT        NOT NULL DEFAULT 'proposed'
                                CHECK (status IN ('proposed','active','rejected','deprecated')),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

COMMENT ON TABLE behavior_rules IS 'behavior_rules table; 365-day retention for rejected/deprecated rows via daily-behavior-rule-prune job per backend-security-design.md §1.3. Active and proposed rows are never auto-pruned.';

CREATE INDEX idx_behavior_rules_workspace_id
    ON behavior_rules(workspace_id) WHERE workspace_id IS NOT NULL;
CREATE INDEX idx_behavior_rules_status
    ON behavior_rules(status);
CREATE INDEX idx_behavior_rules_status_created_at
    ON behavior_rules(status, created_at DESC);
CREATE INDEX idx_behavior_rules_source
    ON behavior_rules(source_type, source_id) WHERE source_id IS NOT NULL;
