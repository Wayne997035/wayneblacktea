-- Guard events: immutable log of every PreToolUse invocation observed by wbt-guard.
-- Rows are append-only; no UPDATE ever touches this table.
-- wbt-guard is observe-only (P0a-β); would_deny records what *would* have been
-- denied but is never acted on at this tier.

CREATE TABLE IF NOT EXISTS guard_events (
    id           UUID        PRIMARY KEY DEFAULT uuid_generate_v4(),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    session_id   TEXT,
    tool_name    TEXT        NOT NULL,
    tool_input   JSONB       NOT NULL,
    cwd          TEXT,
    repo_name    TEXT,
    risk_tier    SMALLINT    NOT NULL,
    risk_reason  TEXT,
    would_deny   BOOLEAN     NOT NULL,
    matcher      TEXT        NOT NULL,
    bypass_id    UUID        NULL
);

-- Query patterns:
--   1. Latest events for dashboards / time-ordered review.
--   2. Per-repo audit trail (repo_name filter + time order).
--   3. Fast filter for events that would have been denied.

CREATE INDEX idx_guard_events_created_at
    ON guard_events (created_at DESC);

CREATE INDEX idx_guard_events_repo_created
    ON guard_events (repo_name, created_at DESC);

CREATE INDEX idx_guard_events_would_deny
    ON guard_events (would_deny)
    WHERE would_deny = TRUE;
