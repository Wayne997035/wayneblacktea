-- SQLite parity for 000059_discipline_events_m8
-- TEXT replaces UUID/JSONB; no gen_random_uuid() (application-generated UUID).
-- Partial WHERE clauses are supported by SQLite 3.8.9+.
--
-- Retention: 90-day TTL enforced by DisciplineEventM8Store.PruneOlderThan
-- (TEXT RFC3339 comparison) per backend-security-design.md §1.3.

CREATE TABLE IF NOT EXISTS discipline_events_m8 (
    id           TEXT    PRIMARY KEY,
    workspace_id TEXT,
    event_type   TEXT    NOT NULL
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
    severity     TEXT    NOT NULL DEFAULT 'warn'
                    CHECK (severity IN ('info', 'warn', 'error')),
    detail       TEXT    NOT NULL DEFAULT 'null',
    resolved_at  TEXT,
    created_at   TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE INDEX IF NOT EXISTS idx_discipline_events_m8_open
    ON discipline_events_m8 (workspace_id, created_at)
    WHERE resolved_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_discipline_events_m8_created_at
    ON discipline_events_m8 (created_at);
