-- SQLite-dialect schema, applied idempotently at sqlite.New() time.
--
-- Differences vs the canonical Postgres schema:
--   * UUIDs are TEXT (canonical 8-4-4-4-12 lowercase). Generated app-side.
--   * Timestamps are TEXT in RFC3339 (UTC); Postgres TIMESTAMPTZ semantics
--     are emulated by always inserting strftime('%Y-%m-%dT%H:%M:%fZ','now').
--   * TEXT[] columns become TEXT JSON arrays (parsed app-side).
--   * JSONB → TEXT (json1 functions are available since SQLite 3.9).
--   * pgvector embedding column → BLOB (no similarity search in v1; List/Get
--     still work).
--   * pg_trgm / to_tsvector / plainto_tsquery are unsupported; knowledge
--     search uses LIKE in v1. FTS5 wiring is a follow-up.
--
-- Workspace scoping pattern is identical (`WHERE (?1 IS NULL OR workspace_id = ?1)`).

CREATE TABLE IF NOT EXISTS goals (
    id           TEXT PRIMARY KEY,
    workspace_id TEXT,
    title        TEXT NOT NULL,
    description  TEXT,
    status       TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','completed','archived')),
    area         TEXT,
    due_date     TEXT,
    created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    updated_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

CREATE TABLE IF NOT EXISTS projects (
    id           TEXT PRIMARY KEY,
    workspace_id TEXT,
    goal_id      TEXT REFERENCES goals(id),
    name         TEXT NOT NULL UNIQUE,
    title        TEXT NOT NULL,
    description  TEXT,
    status       TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','completed','archived','on_hold')),
    area         TEXT NOT NULL DEFAULT 'projects',
    priority     INTEGER NOT NULL DEFAULT 3 CHECK (priority BETWEEN 1 AND 5),
    created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    updated_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

CREATE TABLE IF NOT EXISTS tasks (
    id           TEXT PRIMARY KEY,
    workspace_id TEXT,
    project_id   TEXT REFERENCES projects(id),
    title        TEXT NOT NULL,
    description  TEXT,
    status       TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','in_progress','completed','cancelled')),
    priority     INTEGER NOT NULL DEFAULT 3 CHECK (priority BETWEEN 1 AND 5),
    importance   INTEGER CHECK (importance BETWEEN 1 AND 3),
    context      TEXT,
    assignee     TEXT,
    due_date     TEXT,
    artifact     TEXT,
    created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    updated_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

CREATE TABLE IF NOT EXISTS activity_log (
    id           TEXT PRIMARY KEY,
    workspace_id TEXT,
    actor        TEXT NOT NULL,
    project_id   TEXT REFERENCES projects(id),
    action       TEXT NOT NULL,
    notes        TEXT,
    created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

CREATE TABLE IF NOT EXISTS session_handoffs (
    id              TEXT PRIMARY KEY,
    workspace_id    TEXT,
    project_id      TEXT REFERENCES projects(id),
    repo_name       TEXT,
    intent          TEXT NOT NULL,
    context_summary TEXT,
    resolved_at     TEXT,
    created_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

CREATE INDEX IF NOT EXISTS idx_projects_status               ON projects(status);
CREATE INDEX IF NOT EXISTS idx_projects_priority             ON projects(priority);
CREATE INDEX IF NOT EXISTS idx_projects_workspace_id         ON projects(workspace_id) WHERE workspace_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_tasks_project_id              ON tasks(project_id);
CREATE INDEX IF NOT EXISTS idx_tasks_status                  ON tasks(status);
CREATE INDEX IF NOT EXISTS idx_tasks_workspace_id            ON tasks(workspace_id) WHERE workspace_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_goals_workspace_id            ON goals(workspace_id) WHERE workspace_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_activity_log_project_id       ON activity_log(project_id);
CREATE INDEX IF NOT EXISTS idx_activity_log_created_at       ON activity_log(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_session_handoffs_unresolved   ON session_handoffs(created_at DESC) WHERE resolved_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_session_handoffs_workspace_id ON session_handoffs(workspace_id) WHERE workspace_id IS NOT NULL;
