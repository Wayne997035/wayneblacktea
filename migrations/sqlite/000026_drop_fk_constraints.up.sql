-- SQLite Migration 000026: drop ALL foreign-key constraints (red line #9)
--
-- Project policy (CLAUDE.md #9): referential integrity MUST live in code.
-- The cascade behaviour previously enforced via ON DELETE CASCADE / SET NULL
-- is replicated in code (see internal/storage/sqlite/gtd.go DeleteTask).
--
-- SQLite cannot DROP a column-level FK in place: ALTER TABLE supports adding
-- columns / renaming but not modifying constraints. The canonical workaround
-- is the "create new table -> INSERT INTO ... SELECT * FROM old -> DROP old
-- -> ALTER RENAME" dance, with PRAGMA foreign_keys=OFF to avoid action on
-- the in-flight rename.
--
-- NOTE: The SQLite-backed runtime applies internal/storage/sqlite/schema.sql
-- directly via embed and does NOT consume migrations/sqlite/. This file
-- exists for postgres<->sqlite numbering parity (rule §6.3) and is correct
-- on its own, but in practice schema.sql is the source of truth for the
-- SQLite runtime.

PRAGMA foreign_keys = OFF;

BEGIN TRANSACTION;

-- 1. projects: drop goal_id REFERENCES goals(id)
CREATE TABLE projects_new (
    id           TEXT PRIMARY KEY,
    workspace_id TEXT,
    goal_id      TEXT,
    name         TEXT NOT NULL UNIQUE,
    title        TEXT NOT NULL,
    description  TEXT,
    status       TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','completed','archived','on_hold')),
    area         TEXT NOT NULL DEFAULT 'projects',
    priority     INTEGER NOT NULL DEFAULT 3 CHECK (priority BETWEEN 1 AND 5),
    created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    updated_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
INSERT INTO projects_new SELECT id, workspace_id, goal_id, name, title, description, status, area, priority, created_at, updated_at FROM projects;
DROP TABLE projects;
ALTER TABLE projects_new RENAME TO projects;
CREATE INDEX IF NOT EXISTS idx_projects_status        ON projects(status);
CREATE INDEX IF NOT EXISTS idx_projects_priority      ON projects(priority);
CREATE INDEX IF NOT EXISTS idx_projects_workspace_id  ON projects(workspace_id) WHERE workspace_id IS NOT NULL;

-- 2. tasks: drop project_id REFERENCES projects(id)
CREATE TABLE tasks_new (
    id           TEXT PRIMARY KEY,
    workspace_id TEXT,
    project_id   TEXT,
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
INSERT INTO tasks_new SELECT id, workspace_id, project_id, title, description, status, priority, importance, context, assignee, due_date, artifact, created_at, updated_at FROM tasks;
DROP TABLE tasks;
ALTER TABLE tasks_new RENAME TO tasks;
CREATE INDEX IF NOT EXISTS idx_tasks_project_id   ON tasks(project_id);
CREATE INDEX IF NOT EXISTS idx_tasks_status       ON tasks(status);
CREATE INDEX IF NOT EXISTS idx_tasks_workspace_id ON tasks(workspace_id) WHERE workspace_id IS NOT NULL;

-- 3. activity_log: drop project_id REFERENCES projects(id)
CREATE TABLE activity_log_new (
    id           TEXT PRIMARY KEY,
    workspace_id TEXT,
    actor        TEXT NOT NULL,
    project_id   TEXT,
    action       TEXT NOT NULL,
    notes        TEXT,
    created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
INSERT INTO activity_log_new SELECT id, workspace_id, actor, project_id, action, notes, created_at FROM activity_log;
DROP TABLE activity_log;
ALTER TABLE activity_log_new RENAME TO activity_log;
CREATE INDEX IF NOT EXISTS idx_activity_log_project_id ON activity_log(project_id);
CREATE INDEX IF NOT EXISTS idx_activity_log_created_at ON activity_log(created_at DESC);

-- 4. session_handoffs: drop project_id REFERENCES projects(id)
CREATE TABLE session_handoffs_new (
    id              TEXT PRIMARY KEY,
    workspace_id    TEXT,
    project_id      TEXT,
    repo_name       TEXT,
    intent          TEXT NOT NULL,
    context_summary TEXT,
    resolved_at     TEXT,
    created_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    summary_text    TEXT,
    embedding       BLOB
);
INSERT INTO session_handoffs_new SELECT id, workspace_id, project_id, repo_name, intent, context_summary, resolved_at, created_at, summary_text, embedding FROM session_handoffs;
DROP TABLE session_handoffs;
ALTER TABLE session_handoffs_new RENAME TO session_handoffs;
CREATE INDEX IF NOT EXISTS idx_session_handoffs_unresolved   ON session_handoffs(created_at DESC) WHERE resolved_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_session_handoffs_workspace_id ON session_handoffs(workspace_id) WHERE workspace_id IS NOT NULL;

-- 5. decisions: drop project_id REFERENCES projects(id)
CREATE TABLE decisions_new (
    id           TEXT PRIMARY KEY,
    workspace_id TEXT,
    project_id   TEXT,
    repo_name    TEXT,
    title        TEXT NOT NULL,
    context      TEXT NOT NULL,
    decision     TEXT NOT NULL,
    rationale    TEXT NOT NULL,
    alternatives TEXT,
    created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
INSERT INTO decisions_new SELECT id, workspace_id, project_id, repo_name, title, context, decision, rationale, alternatives, created_at FROM decisions;
DROP TABLE decisions;
ALTER TABLE decisions_new RENAME TO decisions;
CREATE INDEX IF NOT EXISTS idx_decisions_project_id   ON decisions(project_id);
CREATE INDEX IF NOT EXISTS idx_decisions_repo_name    ON decisions(repo_name);
CREATE INDEX IF NOT EXISTS idx_decisions_created_at   ON decisions(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_decisions_workspace_id ON decisions(workspace_id) WHERE workspace_id IS NOT NULL;

-- 6. review_schedule: drop concept_id REFERENCES concepts(id) ON DELETE CASCADE
CREATE TABLE review_schedule_new (
    id             TEXT PRIMARY KEY,
    workspace_id   TEXT,
    concept_id     TEXT NOT NULL,
    stability      REAL NOT NULL DEFAULT 1.0,
    difficulty     REAL NOT NULL DEFAULT 0.3,
    due_date       TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    last_review_at TEXT,
    review_count   INTEGER NOT NULL DEFAULT 0,
    created_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    updated_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
INSERT INTO review_schedule_new SELECT id, workspace_id, concept_id, stability, difficulty, due_date, last_review_at, review_count, created_at, updated_at FROM review_schedule;
DROP TABLE review_schedule;
ALTER TABLE review_schedule_new RENAME TO review_schedule;
CREATE INDEX IF NOT EXISTS idx_review_schedule_due_date     ON review_schedule(due_date ASC);
CREATE INDEX IF NOT EXISTS idx_review_schedule_concept_id   ON review_schedule(concept_id);
CREATE INDEX IF NOT EXISTS idx_review_schedule_workspace_id ON review_schedule(workspace_id) WHERE workspace_id IS NOT NULL;

-- 7. work_sessions: drop project_id REFERENCES projects(id) ON DELETE SET NULL
--                  and current_task_id REFERENCES tasks(id) ON DELETE SET NULL
CREATE TABLE work_sessions_new (
    id                  TEXT        PRIMARY KEY,
    workspace_id        TEXT        NOT NULL,
    repo_name           TEXT        NOT NULL,
    project_id          TEXT,
    title               TEXT        NOT NULL,
    goal                TEXT        NOT NULL,
    status              TEXT        NOT NULL CHECK (status IN (
                            'planned','in_progress','checkpointed',
                            'completed','cancelled','archived')),
    source              TEXT        NOT NULL CHECK (source IN ('manual','confirm_plan','hook','other')),
    confirmed_plan_id   TEXT,
    current_task_id     TEXT,
    final_summary       TEXT,
    started_at          TEXT,
    last_checkpoint_at  TEXT,
    completed_at        TEXT,
    created_at          TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    updated_at          TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
INSERT INTO work_sessions_new SELECT id, workspace_id, repo_name, project_id, title, goal, status, source, confirmed_plan_id, current_task_id, final_summary, started_at, last_checkpoint_at, completed_at, created_at, updated_at FROM work_sessions;
DROP TABLE work_sessions;
ALTER TABLE work_sessions_new RENAME TO work_sessions;
CREATE UNIQUE INDEX IF NOT EXISTS idx_work_sessions_one_active
    ON work_sessions(workspace_id, repo_name)
    WHERE status = 'in_progress';
CREATE INDEX IF NOT EXISTS idx_work_sessions_workspace_id
    ON work_sessions(workspace_id) WHERE workspace_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_work_sessions_repo_name
    ON work_sessions(workspace_id, repo_name, created_at);
CREATE INDEX IF NOT EXISTS idx_work_sessions_status
    ON work_sessions(workspace_id, status);

-- 8. work_session_tasks: drop session_id REFERENCES work_sessions(id) ON DELETE CASCADE
--                        and task_id REFERENCES tasks(id) ON DELETE CASCADE
CREATE TABLE work_session_tasks_new (
    session_id  TEXT NOT NULL,
    task_id     TEXT NOT NULL,
    role        TEXT NOT NULL CHECK (role IN ('primary','follow_up','blocker','generated')),
    created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    PRIMARY KEY (session_id, task_id)
);
INSERT INTO work_session_tasks_new SELECT session_id, task_id, role, created_at FROM work_session_tasks;
DROP TABLE work_session_tasks;
ALTER TABLE work_session_tasks_new RENAME TO work_session_tasks;
CREATE INDEX IF NOT EXISTS idx_work_session_tasks_session_id ON work_session_tasks(session_id);
CREATE INDEX IF NOT EXISTS idx_work_session_tasks_task_id    ON work_session_tasks(task_id);

COMMIT;

PRAGMA foreign_keys = ON;
