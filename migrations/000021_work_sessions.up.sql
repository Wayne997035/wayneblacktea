-- Migration 000021: work_sessions table
--
-- Creates the work_session first-class data model for P0a Session Core.
-- A work_session represents a bounded unit of intentional work tracked
-- from confirm_plan through finish_work.
--
-- Partial unique index (status='in_progress') enforces the invariant that
-- at most one session per workspace+repo is active at a time.

CREATE TABLE IF NOT EXISTS work_sessions (
    id                  UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id        UUID        NOT NULL,
    repo_name           TEXT        NOT NULL,
    project_id          UUID,        -- referential integrity in code (red line #9; see migration 000026)
    title               TEXT        NOT NULL,
    goal                TEXT        NOT NULL,
    status              TEXT        NOT NULL CHECK (status IN (
                            'planned','in_progress','checkpointed',
                            'completed','cancelled','archived')),
    source              TEXT        NOT NULL CHECK (source IN ('manual','confirm_plan','hook','other')),
    -- confirmed_plan_id is nullable and has no FK intentionally: it is reserved
    -- for future linking to a first-class plans table (not yet implemented in P0a-α).
    -- Do not set this field until the plans table exists and is migrated.
    confirmed_plan_id   UUID        NULL,
    current_task_id     UUID,        -- referential integrity in code (red line #9); GTD store NULLs this on parent task delete
    final_summary       TEXT        NULL,
    started_at          TIMESTAMPTZ NULL,
    last_checkpoint_at  TIMESTAMPTZ NULL,
    completed_at        TIMESTAMPTZ NULL,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Partial unique index: only one in_progress session per workspace+repo.
-- Postgres supports partial unique indexes natively.
CREATE UNIQUE INDEX idx_work_sessions_one_active
    ON work_sessions(workspace_id, repo_name)
    WHERE status = 'in_progress';

CREATE INDEX idx_work_sessions_workspace_id
    ON work_sessions(workspace_id);

CREATE INDEX idx_work_sessions_repo_name
    ON work_sessions(workspace_id, repo_name, created_at DESC);

CREATE INDEX idx_work_sessions_status
    ON work_sessions(workspace_id, status);
