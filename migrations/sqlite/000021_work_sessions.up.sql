-- SQLite Migration 000021: work_sessions table
--
-- SQLite differences vs Postgres:
--   * UUIDs are TEXT (app-generated via uuid.New()).
--   * Timestamps are TEXT RFC3339 UTC.
--   * Partial unique index supported natively in SQLite (WHERE clause).
--   * gen_random_uuid() not available; app supplies the UUID.

CREATE TABLE IF NOT EXISTS work_sessions (
    id                  TEXT        PRIMARY KEY,
    workspace_id        TEXT        NOT NULL,
    repo_name           TEXT        NOT NULL,
    project_id          TEXT        REFERENCES projects(id) ON DELETE SET NULL,
    title               TEXT        NOT NULL,
    goal                TEXT        NOT NULL,
    status              TEXT        NOT NULL CHECK (status IN (
                            'planned','in_progress','checkpointed',
                            'completed','cancelled','archived')),
    source              TEXT        NOT NULL CHECK (source IN ('manual','confirm_plan','hook','other')),
    confirmed_plan_id   TEXT        NULL,
    current_task_id     TEXT        REFERENCES tasks(id) ON DELETE SET NULL,
    final_summary       TEXT        NULL,
    started_at          TEXT        NULL,
    last_checkpoint_at  TEXT        NULL,
    completed_at        TEXT        NULL,
    created_at          TEXT        NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    updated_at          TEXT        NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

-- Partial unique index: only one in_progress session per workspace+repo.
CREATE UNIQUE INDEX IF NOT EXISTS idx_work_sessions_one_active
    ON work_sessions(workspace_id, repo_name)
    WHERE status = 'in_progress';

CREATE INDEX IF NOT EXISTS idx_work_sessions_workspace_id
    ON work_sessions(workspace_id) WHERE workspace_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_work_sessions_repo_name
    ON work_sessions(workspace_id, repo_name, created_at);

CREATE INDEX IF NOT EXISTS idx_work_sessions_status
    ON work_sessions(workspace_id, status);
