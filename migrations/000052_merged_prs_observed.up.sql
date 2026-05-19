-- Migration 000052: persist observed merged PRs so the daily fuzzy-match
-- candidate detector (Phase 2 of reconcile_merged_prs — sprint
-- feature/0519-gtd-reconcile-phase2, GTD-fix 10/12) can correlate them
-- against pending tasks whose branch_name / pr_url linkage is NULL.
--
-- Retention: 30 days. Implemented via the scheduler's daily 03:00 prune job
-- (DELETE WHERE observed_at < NOW() - INTERVAL '30 days') so the table cannot
-- grow unbounded. Per backend-security-design.md §1.3 — observability tables
-- MUST have a working retention policy in the same PR that introduces them.
--
-- No FOREIGN KEY constraints (CLAUDE.md red-line §9): `repo` is a free-text
-- slug ("owner/name"), not a typed reference into the workspace.repos table,
-- and the row's only reason for existence is the audit trail, not joined reads.
-- Idempotency comes from the unique index on url; ON CONFLICT (url) updates
-- observed_at so a second post of the same PR refreshes the timestamp.
CREATE TABLE merged_prs_observed (
    id            UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    workspace_id  UUID,
    repo          TEXT        NOT NULL,
    url           TEXT        NOT NULL,
    head_ref      TEXT,
    title         TEXT,
    body_excerpt  TEXT,
    merged_at     TIMESTAMPTZ NOT NULL,
    observed_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX idx_merged_prs_observed_url ON merged_prs_observed(url);
CREATE INDEX idx_merged_prs_observed_observed_at_desc ON merged_prs_observed(observed_at DESC);
CREATE INDEX idx_merged_prs_observed_workspace_id ON merged_prs_observed(workspace_id) WHERE workspace_id IS NOT NULL;
