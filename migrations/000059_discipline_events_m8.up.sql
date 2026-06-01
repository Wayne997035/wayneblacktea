-- Migration: 000059_discipline_events_m8
-- Purpose: Persistent watchdog meta-cognition events (Memory-8).
--
-- NOTE: This is intentionally a SEPARATE table from the existing
-- "discipline_events" table (migration 000035) which records MCP tool-call
-- middleware drift signals. This new table records higher-level
-- "self-monitoring" detections (stuck tasks, unlogged decisions, etc.)
-- run by the analyze_agent_behavior MCP tool.
--
-- Retention: 90-day TTL enforced by the daily-discipline-event-m8-prune
-- scheduler job (DELETE WHERE created_at < NOW() - INTERVAL '90 days')
-- per backend-security-design.md §1.3.

CREATE TABLE discipline_events_m8 (
    id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID,
    event_type   TEXT        NOT NULL
                    CHECK (event_type IN (
                        'stuck_task',
                        'unlogged_decision',
                        'plan_no_task',
                        'proposal_fail',
                        'stale_handoff',
                        'repeated_correction',
                        'task_no_outcome',
                        'decision_no_reflection'
                    )),
    severity     TEXT        NOT NULL DEFAULT 'warn'
                    CHECK (severity IN ('info', 'warn', 'error')),
    detail       JSONB       NOT NULL DEFAULT 'null'::jsonb,
    resolved_at  TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Hot-path index for detect_unclosed_loops: open events per workspace.
CREATE INDEX idx_discipline_events_m8_open
    ON discipline_events_m8 (workspace_id, created_at DESC)
    WHERE resolved_at IS NULL;

-- Secondary index for TTL prune job.
CREATE INDEX idx_discipline_events_m8_created_at
    ON discipline_events_m8 (created_at DESC);

COMMENT ON TABLE discipline_events_m8
    IS 'Watchdog meta-cognition events (M8). 90-day retention via daily-discipline-event-m8-prune scheduler job per backend-security-design.md §1.3.';
