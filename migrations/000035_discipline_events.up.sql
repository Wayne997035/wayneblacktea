-- Discipline events: per-MCP-tool-call audit trail used by the system_health
-- drift-detection signal. Replaces the cancelled PreToolUse hook approach
-- (decision e5310a15) with a portable MCP-native mechanism.
--
-- Every successful mutating MCP tool call writes a row here; system_health
-- counts mutating events that lack a preceding log_decision / confirm_plan
-- in the same session within a 15-minute window and surfaces them as
-- "forgotten signals" — a portable replacement for the PreToolUse hook.
--
-- No FK constraints (CLAUDE.md #9, backend-security-design.md §1.1).
-- linked_decision_id / workspace_id are referential-by-code only.
--
-- TTL: 30 days. Enforced via `task discipline-prune` (build/Taskfile.yml),
-- mirroring the guard_events pruning policy. See backend-security-design.md
-- §1.3: observability tables MUST ship with a working retention policy.
CREATE TABLE IF NOT EXISTS discipline_events (
    id                 BIGSERIAL PRIMARY KEY,
    session_id         TEXT      NOT NULL,
    repo_name          TEXT,
    tool_name          TEXT      NOT NULL,
    is_mutating        BOOLEAN   NOT NULL DEFAULT FALSE,
    observed_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    linked_decision_id UUID,
    workspace_id       UUID
);

CREATE INDEX IF NOT EXISTS idx_discipline_events_observed_at
    ON discipline_events(observed_at DESC);

CREATE INDEX IF NOT EXISTS idx_discipline_events_workspace
    ON discipline_events(workspace_id, observed_at DESC)
    WHERE workspace_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_discipline_events_session_observed
    ON discipline_events(session_id, observed_at DESC);

COMMENT ON TABLE discipline_events IS
    'MCP tool-call audit trail for meta-rule drift detection; 30-day TTL via task discipline-prune';
