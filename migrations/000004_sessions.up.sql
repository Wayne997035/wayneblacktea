CREATE TABLE session_handoffs (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    project_id      UUID, -- referential integrity in code (red line #9; see migration 000026).
                          -- A future DeleteProject handler MUST cleanup
                          -- session_handoffs referencing this project_id
                          -- (mirrors GTDStore.DeleteTask precedent for tasks).
    repo_name       TEXT,
    intent          TEXT NOT NULL,
    context_summary TEXT,
    resolved_at     TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_session_handoffs_resolved ON session_handoffs(created_at DESC) WHERE resolved_at IS NULL;
CREATE INDEX idx_session_handoffs_created ON session_handoffs(created_at DESC);
