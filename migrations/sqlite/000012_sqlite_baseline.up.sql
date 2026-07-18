-- SQLite baseline: schema state as of Postgres migration 000012 (concept_status),
-- the point at which per-migration SQLite twins (migrations/sqlite/000013+) begin.
--
-- Reconstructed by subtraction from the current internal/storage/sqlite/schema.sql:
-- every column/table/index introduced by a later numbered twin (000013-000068) has
-- been removed here and is re-added by its own twin file when the migration runner
-- replays the sequence. Cross-validated against migrations/sqlite/000026_drop_fk_constraints
-- (a table-rebuild migration whose "_new" table definitions capture the exact column
-- set in force at that point in history) and against the original Postgres migrations
-- 000001-000012.
--
-- Dialect notes (see internal/storage/sqlite/schema.sql header for full list):
--   * UUIDs are TEXT (canonical 8-4-4-4-12 lowercase), generated app-side.
--   * Timestamps are TEXT in RFC3339 (UTC) via strftime('%Y-%m-%dT%H:%M:%fZ','now').
--   * No FOREIGN KEY constraints anywhere (CLAUDE.md red-line #9); referential
--     integrity is enforced in code.
--
-- repos.name: baseline uses an explicit named UNIQUE INDEX (idx_repos_name_unique)
-- rather than a column-level UNIQUE constraint. SQLite backs a column-level UNIQUE
-- with an internal sqlite_autoindex_* index that CANNOT be dropped via DROP INDEX
-- ("index associated with UNIQUE or PRIMARY KEY constraint cannot be dropped") —
-- confirmed empirically. Migration 000028 needs to drop and replace this index with
-- a per-workspace composite unique index, so the baseline must use a plain named
-- index to keep 000028 executable.

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
    goal_id      TEXT, -- referential integrity in code (red line #9)
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
    project_id   TEXT, -- referential integrity in code (red line #9)
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
    project_id   TEXT, -- referential integrity in code (red line #9)
    action       TEXT NOT NULL,
    notes        TEXT,
    created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

CREATE TABLE IF NOT EXISTS session_handoffs (
    id              TEXT PRIMARY KEY,
    workspace_id    TEXT,
    project_id      TEXT, -- referential integrity in code (red line #9)
    repo_name       TEXT,
    intent          TEXT NOT NULL,
    context_summary TEXT,
    resolved_at     TEXT,
    created_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

CREATE INDEX IF NOT EXISTS idx_projects_status          ON projects(status);
CREATE INDEX IF NOT EXISTS idx_projects_priority        ON projects(priority);
CREATE INDEX IF NOT EXISTS idx_projects_workspace_id    ON projects(workspace_id) WHERE workspace_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_tasks_project_id         ON tasks(project_id);
CREATE INDEX IF NOT EXISTS idx_tasks_status             ON tasks(status);
CREATE INDEX IF NOT EXISTS idx_tasks_workspace_id       ON tasks(workspace_id) WHERE workspace_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_goals_workspace_id       ON goals(workspace_id) WHERE workspace_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_activity_log_project_id  ON activity_log(project_id);
CREATE INDEX IF NOT EXISTS idx_activity_log_created_at  ON activity_log(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_session_handoffs_unresolved   ON session_handoffs(created_at DESC) WHERE resolved_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_session_handoffs_workspace_id ON session_handoffs(workspace_id) WHERE workspace_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS repos (
    id                TEXT PRIMARY KEY,
    workspace_id      TEXT,
    name              TEXT NOT NULL,
    path              TEXT,
    description       TEXT,
    language          TEXT,
    status            TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','archived','on_hold')),
    current_branch    TEXT,
    known_issues      TEXT NOT NULL DEFAULT '[]',
    next_planned_step TEXT,
    last_activity     TEXT,
    created_at        TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    updated_at        TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_repos_name_unique ON repos(name);

CREATE TABLE IF NOT EXISTS decisions (
    id                 TEXT PRIMARY KEY,
    workspace_id       TEXT,
    project_id         TEXT, -- referential integrity in code (red line #9)
    repo_name          TEXT,
    title              TEXT NOT NULL,
    context            TEXT NOT NULL,
    decision           TEXT NOT NULL,
    rationale          TEXT NOT NULL,
    alternatives       TEXT,
    created_at         TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

CREATE TABLE IF NOT EXISTS knowledge_items (
    id               TEXT PRIMARY KEY,
    workspace_id     TEXT,
    type             TEXT NOT NULL CHECK (type IN ('article','til','bookmark','zettelkasten')),
    title            TEXT NOT NULL,
    content          TEXT NOT NULL DEFAULT '',
    url              TEXT,
    tags             TEXT NOT NULL DEFAULT '[]',
    embedding        BLOB,
    created_at       TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    updated_at       TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    source           TEXT NOT NULL DEFAULT 'manual',
    learning_value   INTEGER CHECK (learning_value BETWEEN 1 AND 5)
);

CREATE TABLE IF NOT EXISTS concepts (
    id               TEXT PRIMARY KEY,
    workspace_id     TEXT,
    title            TEXT NOT NULL,
    content          TEXT NOT NULL,
    tags             TEXT NOT NULL DEFAULT '[]',
    status           TEXT NOT NULL DEFAULT 'active',
    created_at       TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    updated_at       TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

CREATE TABLE IF NOT EXISTS review_schedule (
    id             TEXT PRIMARY KEY,
    workspace_id   TEXT,
    concept_id     TEXT NOT NULL, -- referential integrity in code (red line #9)
    stability      REAL NOT NULL DEFAULT 1.0,
    difficulty     REAL NOT NULL DEFAULT 0.3,
    due_date       TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    last_review_at TEXT,
    review_count   INTEGER NOT NULL DEFAULT 0,
    created_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    updated_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

CREATE TABLE IF NOT EXISTS pending_proposals (
    id            TEXT PRIMARY KEY,
    workspace_id  TEXT,
    type          TEXT NOT NULL CHECK (type IN ('goal','project','task','concept','knowledge','playbook','decision')),
    payload       TEXT NOT NULL,
    status        TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','accepted','rejected')),
    proposed_by   TEXT,
    created_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    resolved_at   TEXT
);

CREATE INDEX IF NOT EXISTS idx_repos_status                 ON repos(status);
CREATE INDEX IF NOT EXISTS idx_repos_last_activity          ON repos(last_activity DESC);
CREATE INDEX IF NOT EXISTS idx_repos_workspace_id           ON repos(workspace_id) WHERE workspace_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_decisions_project_id         ON decisions(project_id);
CREATE INDEX IF NOT EXISTS idx_decisions_repo_name          ON decisions(repo_name);
CREATE INDEX IF NOT EXISTS idx_decisions_created_at         ON decisions(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_decisions_workspace_id       ON decisions(workspace_id) WHERE workspace_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_knowledge_type               ON knowledge_items(type);
CREATE INDEX IF NOT EXISTS idx_knowledge_created_at         ON knowledge_items(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_knowledge_items_workspace_id ON knowledge_items(workspace_id) WHERE workspace_id IS NOT NULL;

-- FTS5 virtual table + triggers (SQLite-only; no Postgres counterpart, never
-- received its own numbered twin — added directly to schema.sql). Included in
-- the baseline per design decision: it has no later twin to reintroduce it.
CREATE VIRTUAL TABLE IF NOT EXISTS knowledge_items_fts
    USING fts5(
        title, content,
        content='knowledge_items',
        content_rowid='rowid',
        tokenize='porter unicode61'
    );

CREATE TRIGGER IF NOT EXISTS knowledge_items_ai AFTER INSERT ON knowledge_items BEGIN
    INSERT INTO knowledge_items_fts(rowid, title, content)
    VALUES (new.rowid, new.title, new.content);
END;

CREATE TRIGGER IF NOT EXISTS knowledge_items_ad AFTER DELETE ON knowledge_items BEGIN
    INSERT INTO knowledge_items_fts(knowledge_items_fts, rowid, title, content)
    VALUES ('delete', old.rowid, old.title, old.content);
END;

CREATE TRIGGER IF NOT EXISTS knowledge_items_au AFTER UPDATE ON knowledge_items BEGIN
    INSERT INTO knowledge_items_fts(knowledge_items_fts, rowid, title, content)
    VALUES ('delete', old.rowid, old.title, old.content);
    INSERT INTO knowledge_items_fts(rowid, title, content)
    VALUES (new.rowid, new.title, new.content);
END;

CREATE INDEX IF NOT EXISTS idx_concepts_workspace_id            ON concepts(workspace_id) WHERE workspace_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_review_schedule_due_date         ON review_schedule(due_date ASC);
CREATE INDEX IF NOT EXISTS idx_review_schedule_concept_id       ON review_schedule(concept_id);
CREATE INDEX IF NOT EXISTS idx_review_schedule_workspace_id     ON review_schedule(workspace_id) WHERE workspace_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_pending_proposals_status_pending ON pending_proposals(created_at DESC) WHERE status = 'pending';
CREATE INDEX IF NOT EXISTS idx_pending_proposals_workspace_id   ON pending_proposals(workspace_id) WHERE workspace_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_pending_proposals_type           ON pending_proposals(type);
