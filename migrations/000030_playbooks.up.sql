CREATE TABLE IF NOT EXISTS playbooks (
    id                  UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id        UUID,
    trigger_pattern     TEXT        NOT NULL,
    action_template     TEXT        NOT NULL,
    source_decision_ids UUID[]      NOT NULL DEFAULT '{}',
    confidence          NUMERIC(5,2) NOT NULL DEFAULT 0.50,
    hits                INTEGER     NOT NULL DEFAULT 0,
    last_used_at        TIMESTAMPTZ,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_playbooks_workspace_id ON playbooks (workspace_id);
CREATE INDEX IF NOT EXISTS idx_playbooks_confidence   ON playbooks (confidence DESC);
