-- Mirrored from migrations/000035_discipline_events. MCP tool-call audit
-- trail used by system_health drift detection.
-- No FK constraints (CLAUDE.md #9). 30-day TTL is enforced app-side / via
-- scheduled DELETE on Postgres; SQLite is dev-local single-tenant so growth
-- is naturally bounded.
CREATE TABLE IF NOT EXISTS discipline_events (
    id                 INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id         TEXT    NOT NULL,
    repo_name          TEXT,
    tool_name          TEXT    NOT NULL,
    is_mutating        INTEGER NOT NULL DEFAULT 0,
    observed_at        TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    linked_decision_id TEXT,
    workspace_id       TEXT
);

CREATE INDEX IF NOT EXISTS idx_discipline_events_observed_at
    ON discipline_events(observed_at DESC);

CREATE INDEX IF NOT EXISTS idx_discipline_events_workspace
    ON discipline_events(workspace_id, observed_at DESC)
    WHERE workspace_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_discipline_events_session_observed
    ON discipline_events(session_id, observed_at DESC);
