-- Project status snapshot store.
-- Each row is a Haiku-generated structured summary of one project's current
-- state (sprint progress, gap analysis, pending work). Rows are derived /
-- computed data — not user-authored knowledge — so they bypass the
-- pending_proposals review gate (see snapshot/generator.go ARCHITECTURAL NOTE).
--
-- source DEFAULT 'auto-status-snapshot' distinguishes cron-generated rows from
-- any future manual overrides.
-- 24-hour TTL is enforced app-side in snapshot/store.go (LatestFresh).

CREATE TABLE project_status_snapshots (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    slug                 TEXT NOT NULL,
    workspace_id         UUID,
    generated_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    sprint_summary       TEXT,
    gap_analysis         TEXT,
    sota_catchup_pct     INT,
    pending_summary      TEXT,
    source_decision_ids  UUID[],
    embedding            BYTEA,
    source               TEXT NOT NULL DEFAULT 'auto-status-snapshot'
);

CREATE INDEX idx_status_snapshots_slug_time
    ON project_status_snapshots (slug, generated_at DESC);
