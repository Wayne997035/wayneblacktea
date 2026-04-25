CREATE TABLE session_handoffs (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    project_id      UUID REFERENCES projects(id),
    repo_name       TEXT,
    intent          TEXT NOT NULL,
    context_summary TEXT,
    resolved_at     TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_session_handoffs_resolved ON session_handoffs(resolved_at) WHERE resolved_at IS NULL;
CREATE INDEX idx_session_handoffs_created ON session_handoffs(created_at DESC);
