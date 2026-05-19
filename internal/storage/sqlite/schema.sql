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
    goal_id      TEXT, -- referential integrity in code (red line #9); if a DeleteGoal handler is added it MUST cleanup downstream tables
    name         TEXT NOT NULL UNIQUE,
    title        TEXT NOT NULL,
    description  TEXT,
    status       TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','completed','archived','on_hold')),
    area         TEXT NOT NULL DEFAULT 'projects',
    priority     INTEGER NOT NULL DEFAULT 3 CHECK (priority BETWEEN 1 AND 5),
    repo_name    TEXT, -- optional repo binding for workspace overview drill-down (migration 000037 parity); referential integrity in code per red line #9
    created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    updated_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

CREATE TABLE IF NOT EXISTS tasks (
    id           TEXT PRIMARY KEY,
    workspace_id TEXT,
    project_id   TEXT, -- referential integrity in code (red line #9); if a DeleteProject handler is added it MUST cleanup downstream tables
    title        TEXT NOT NULL,
    description  TEXT,
    status       TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','in_progress','completed','cancelled')),
    priority     INTEGER NOT NULL DEFAULT 3 CHECK (priority BETWEEN 1 AND 5),
    importance   INTEGER CHECK (importance BETWEEN 1 AND 3),
    context      TEXT,
    assignee     TEXT,
    due_date     TEXT,
    artifact     TEXT,
    checklist    TEXT NOT NULL DEFAULT '[]', -- JSON array of ChecklistItem; parity with Postgres jsonb checklist column (migration 000043)
    kind         TEXT NOT NULL DEFAULT 'general' CHECK (kind IN ('general','fix-pr','feature','refactor','research','chore')), -- task kind; parity with Postgres kind column (migration 000044)
    branch_name    TEXT,                       -- git branch name for the task (migration 000047)
    pr_url         TEXT,                       -- GitHub PR URL for the task (migration 000047)
    commit_shas    TEXT NOT NULL DEFAULT '[]', -- JSON array of commit SHAs (migration 000047)
    vision_item_id TEXT,                       -- vision item this task was promoted from (migration 000050); referential integrity in code (red line #9)
    created_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    updated_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

CREATE TABLE IF NOT EXISTS activity_log (
    id           TEXT PRIMARY KEY,
    workspace_id TEXT,
    actor        TEXT NOT NULL,
    project_id   TEXT, -- referential integrity in code (red line #9); if a DeleteProject handler is added it MUST cleanup downstream tables
    action       TEXT NOT NULL,
    notes        TEXT,
    created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

CREATE TABLE IF NOT EXISTS session_handoffs (
    id              TEXT PRIMARY KEY,
    workspace_id    TEXT,
    project_id      TEXT, -- referential integrity in code (red line #9); if a DeleteProject handler is added it MUST cleanup downstream tables
    repo_name       TEXT,
    intent          TEXT NOT NULL,
    context_summary TEXT,
    resolved_at     TEXT,
    created_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    summary_text    TEXT,
    embedding       BLOB,
    next_actions    TEXT NOT NULL DEFAULT '[]' -- JSON array of NextAction; parity with Postgres jsonb next_actions column (migration 000046)
);

CREATE INDEX IF NOT EXISTS idx_projects_status               ON projects(status);
CREATE INDEX IF NOT EXISTS idx_projects_priority             ON projects(priority);
CREATE INDEX IF NOT EXISTS idx_projects_workspace_id         ON projects(workspace_id) WHERE workspace_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_projects_workspace_repo_name  ON projects(workspace_id, repo_name) WHERE repo_name IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_tasks_project_id              ON tasks(project_id);
CREATE INDEX IF NOT EXISTS idx_tasks_status                  ON tasks(status);
CREATE INDEX IF NOT EXISTS idx_tasks_workspace_id            ON tasks(workspace_id) WHERE workspace_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_tasks_vision_item_id          ON tasks(vision_item_id);
CREATE INDEX IF NOT EXISTS idx_goals_workspace_id            ON goals(workspace_id) WHERE workspace_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_activity_log_project_id       ON activity_log(project_id);
CREATE INDEX IF NOT EXISTS idx_activity_log_created_at       ON activity_log(created_at DESC);
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

CREATE TABLE IF NOT EXISTS decisions (
    id           TEXT PRIMARY KEY,
    workspace_id TEXT,
    project_id   TEXT, -- referential integrity in code (red line #9); if a DeleteProject handler is added it MUST cleanup downstream tables
    repo_name    TEXT,
    title        TEXT NOT NULL,
    context      TEXT NOT NULL,
    decision     TEXT NOT NULL,
    rationale    TEXT NOT NULL,
    alternatives TEXT,
    created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    task_id      TEXT          -- cross-domain ref (migration 000048); referential integrity in code (red line #9)
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
    learning_value   INTEGER CHECK (learning_value BETWEEN 1 AND 5),
    -- Ebbinghaus decay fields (migration 000019)
    importance       REAL    NOT NULL DEFAULT 0.5,
    recall_count     INTEGER NOT NULL DEFAULT 0,
    last_recalled_at TEXT,
    base_lambda      REAL    NOT NULL DEFAULT 0.1,
    archived_at      TEXT,
    -- K1 hierarchy fields (migration 000027): self-referential parent, no FK (CLAUDE.md #9)
    parent_id        TEXT    DEFAULT NULL, -- referential integrity in code (red line #9)
    heading_path     TEXT    DEFAULT NULL, -- e.g. "Introduction › Key Concepts"
    heading_level    INTEGER DEFAULT NULL, -- 0=root, 1=section, 2=subsection
    -- Cross-domain reference fields (migration 000049); referential integrity in code (red line #9)
    project_id       TEXT    DEFAULT NULL,
    task_id          TEXT    DEFAULT NULL,
    decision_id      TEXT    DEFAULT NULL
);

CREATE TABLE IF NOT EXISTS concepts (
    id               TEXT PRIMARY KEY,
    workspace_id     TEXT,
    title            TEXT NOT NULL,
    content          TEXT NOT NULL,
    tags             TEXT NOT NULL DEFAULT '[]',
    status           TEXT NOT NULL DEFAULT 'active',
    created_at       TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    updated_at       TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    -- Ebbinghaus decay fields (migration 000019)
    importance       REAL    NOT NULL DEFAULT 0.5,
    recall_count     INTEGER NOT NULL DEFAULT 0,
    last_recalled_at TEXT,
    base_lambda      REAL    NOT NULL DEFAULT 0.1,
    archived_at      TEXT
);

CREATE TABLE IF NOT EXISTS review_schedule (
    id             TEXT PRIMARY KEY,
    workspace_id   TEXT,
    concept_id     TEXT NOT NULL, -- referential integrity in code (red line #9); if a DeleteConcept handler is added it MUST cleanup review_schedule rows
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
    resolved_at   TEXT,
    reason        TEXT
);

CREATE INDEX IF NOT EXISTS idx_repos_status                         ON repos(status);
CREATE INDEX IF NOT EXISTS idx_repos_last_activity                  ON repos(last_activity DESC);
CREATE INDEX IF NOT EXISTS idx_repos_workspace_id                   ON repos(workspace_id) WHERE workspace_id IS NOT NULL;
-- Per-workspace composite unique (migration 000028): two different workspaces
-- may each have a repo with the same name; only (workspace_id, name) must be
-- unique. Uses COALESCE(workspace_id,'') so that NULL workspace_ids (legacy
-- single-tenant mode) also benefit from uniqueness enforcement — two NULLs
-- would otherwise be considered distinct by SQLite's NULL != NULL semantics.
CREATE UNIQUE INDEX IF NOT EXISTS idx_repos_workspace_name_unique
    ON repos(COALESCE(workspace_id, ''), name);
CREATE INDEX IF NOT EXISTS idx_decisions_project_id                 ON decisions(project_id);
CREATE INDEX IF NOT EXISTS idx_decisions_repo_name                  ON decisions(repo_name);
CREATE INDEX IF NOT EXISTS idx_decisions_created_at                 ON decisions(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_decisions_workspace_id               ON decisions(workspace_id) WHERE workspace_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_decisions_task_id                    ON decisions(task_id);
CREATE INDEX IF NOT EXISTS idx_knowledge_type                       ON knowledge_items(type);
CREATE INDEX IF NOT EXISTS idx_knowledge_created_at                 ON knowledge_items(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_knowledge_items_workspace_id         ON knowledge_items(workspace_id) WHERE workspace_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_knowledge_archived_at                ON knowledge_items(archived_at) WHERE archived_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_knowledge_last_recalled_at           ON knowledge_items(last_recalled_at);
-- K1 hierarchy indexes (migration 000027): partial to avoid bloat on NULL-heavy columns.
CREATE INDEX IF NOT EXISTS idx_knowledge_items_parent_id
    ON knowledge_items(parent_id) WHERE parent_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_knowledge_items_heading_path
    ON knowledge_items(heading_path) WHERE heading_path IS NOT NULL;
-- Cross-domain reference indexes (migration 000049).
CREATE INDEX IF NOT EXISTS idx_knowledge_items_project_id  ON knowledge_items(project_id);
CREATE INDEX IF NOT EXISTS idx_knowledge_items_task_id     ON knowledge_items(task_id);
CREATE INDEX IF NOT EXISTS idx_knowledge_items_decision_id ON knowledge_items(decision_id);

-- FTS5 virtual table (SQLite-only; Postgres uses pg_trgm/tsvector natively via sqlc queries).
-- sqlite-vec (vector ANN) is deferred: modernc.org/sqlite cannot load C extensions.
-- The current brute-force cosine in SearchByCosine is adequate for personal scale (<=200 rows).
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
CREATE INDEX IF NOT EXISTS idx_concepts_workspace_id                ON concepts(workspace_id) WHERE workspace_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_concepts_archived_at                 ON concepts(archived_at) WHERE archived_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_concepts_last_recalled_at            ON concepts(last_recalled_at);
CREATE INDEX IF NOT EXISTS idx_review_schedule_due_date             ON review_schedule(due_date ASC);
CREATE INDEX IF NOT EXISTS idx_review_schedule_concept_id           ON review_schedule(concept_id);
CREATE INDEX IF NOT EXISTS idx_review_schedule_workspace_id         ON review_schedule(workspace_id) WHERE workspace_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_pending_proposals_status_pending     ON pending_proposals(created_at DESC) WHERE status = 'pending';
CREATE INDEX IF NOT EXISTS idx_pending_proposals_workspace_id       ON pending_proposals(workspace_id) WHERE workspace_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_pending_proposals_type               ON pending_proposals(type);

CREATE TABLE IF NOT EXISTS project_arch (
    id              TEXT PRIMARY KEY,
    slug            TEXT NOT NULL UNIQUE,
    summary         TEXT NOT NULL,
    file_map        TEXT NOT NULL DEFAULT '{}',
    last_commit_sha TEXT NOT NULL DEFAULT '',
    updated_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

-- Mirrored from migrations/sqlite/000013_workspace_model_preference.up.sql.
-- Per-workspace AI model preference (sonnet vs haiku). Absence of a row =
-- application default ('claude-sonnet-4-6'); see workspace.DefaultModelPreference.
CREATE TABLE IF NOT EXISTS workspace_preferences (
    workspace_id     TEXT PRIMARY KEY,
    model_preference TEXT NOT NULL DEFAULT 'claude-sonnet-4-6',
    created_at       TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    updated_at       TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

-- Mirrored from migrations/sqlite/000018_project_status_snapshots.up.sql.
-- Haiku-generated project status snapshots (sprint summary, gap analysis, etc.).
-- Derived/computed data — bypasses pending_proposals gate. 24 h TTL enforced app-side.
CREATE TABLE IF NOT EXISTS project_status_snapshots (
    id                   TEXT PRIMARY KEY,
    slug                 TEXT NOT NULL,
    workspace_id         TEXT,
    generated_at         TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    sprint_summary       TEXT,
    gap_analysis         TEXT,
    sota_catchup_pct     INTEGER,
    pending_summary      TEXT,
    source_decision_ids  TEXT NOT NULL DEFAULT '[]',
    embedding            BLOB,
    source               TEXT NOT NULL DEFAULT 'auto-status-snapshot'
);

CREATE INDEX IF NOT EXISTS idx_status_snapshots_slug_time
    ON project_status_snapshots (slug, generated_at DESC);

-- Mirrored from migrations/sqlite/000021_work_sessions.up.sql.
-- First-class work session model for P0a Session Core.
CREATE TABLE IF NOT EXISTS work_sessions (
    id                  TEXT        PRIMARY KEY,
    workspace_id        TEXT        NOT NULL,
    repo_name           TEXT        NOT NULL,
    project_id          TEXT, -- referential integrity in code (red line #9); cleanup on parent delete handled in service layer
    title               TEXT        NOT NULL,
    goal                TEXT        NOT NULL,
    status              TEXT        NOT NULL CHECK (status IN (
                            'planned','in_progress','checkpointed',
                            'completed','cancelled','archived')),
    source              TEXT        NOT NULL CHECK (source IN ('manual','confirm_plan','hook','other')),
    confirmed_plan_id   TEXT        NULL,
    current_task_id     TEXT, -- referential integrity in code (red line #9); GTDStore.DeleteTask NULLs this on parent task delete
    final_summary       TEXT        NULL,
    started_at          TEXT        NULL,
    last_checkpoint_at  TEXT        NULL,
    completed_at        TEXT        NULL,
    created_at          TEXT        NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    updated_at          TEXT        NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_work_sessions_one_active
    ON work_sessions(workspace_id, repo_name)
    WHERE status = 'in_progress';

CREATE INDEX IF NOT EXISTS idx_work_sessions_workspace_id
    ON work_sessions(workspace_id) WHERE workspace_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_work_sessions_repo_name
    ON work_sessions(workspace_id, repo_name, created_at);

CREATE INDEX IF NOT EXISTS idx_work_sessions_status
    ON work_sessions(workspace_id, status);

-- Mirrored from migrations/sqlite/000022_work_session_tasks.up.sql.
-- Links work_sessions to tasks with a role classification.
CREATE TABLE IF NOT EXISTS work_session_tasks (
    -- referential integrity in code (red line #9); GTDStore.DeleteTask cleans up
    -- rows on parent task delete. A future DeleteWorkSession handler MUST do the same.
    session_id  TEXT    NOT NULL,
    task_id     TEXT    NOT NULL,
    role        TEXT    NOT NULL CHECK (role IN ('primary','follow_up','blocker','generated')),
    created_at  TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    PRIMARY KEY (session_id, task_id)
);

CREATE INDEX IF NOT EXISTS idx_work_session_tasks_session_id
    ON work_session_tasks(session_id);

CREATE INDEX IF NOT EXISTS idx_work_session_tasks_task_id
    ON work_session_tasks(task_id);

-- Mirrored from migrations/sqlite/000029_vision_items.up.sql.
-- Vision items: future ideas that can't be acted on now. No FK constraints (CLAUDE.md #9).
-- Referential integrity for workspace_id / project_id / promoted_task_id enforced in code.
CREATE TABLE IF NOT EXISTS vision_items (
    id                  TEXT    PRIMARY KEY,
    workspace_id        TEXT,
    repo_name           TEXT,
    project_id          TEXT,
    title               TEXT    NOT NULL,
    why_blocked         TEXT    NOT NULL,
    depends_on          TEXT    NOT NULL DEFAULT '[]',
    parent_initiative   TEXT,
    status              TEXT    NOT NULL DEFAULT 'open'
                            CHECK (status IN ('open','discussing','maturing','promoted','dismissed')),
    context_md          TEXT,
    promoted_task_id    TEXT,
    last_discussed_at   TEXT,
    created_at          TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

CREATE INDEX IF NOT EXISTS idx_vision_items_status
    ON vision_items(status) WHERE status != 'dismissed';

CREATE INDEX IF NOT EXISTS idx_vision_items_initiative
    ON vision_items(parent_initiative) WHERE parent_initiative IS NOT NULL;

-- Mirrored from migrations/000030_playbooks. Procedural memory: trigger→action rules.
-- No FK constraints (CLAUDE.md #9). Confidence managed in application code.
CREATE TABLE IF NOT EXISTS playbooks (
    id                  TEXT        PRIMARY KEY,
    workspace_id        TEXT,
    trigger_pattern     TEXT        NOT NULL,
    action_template     TEXT        NOT NULL,
    source_decision_ids TEXT        NOT NULL DEFAULT '[]',
    confidence          REAL        NOT NULL DEFAULT 0.50,
    hits                INTEGER     NOT NULL DEFAULT 0,
    last_used_at        TEXT,
    created_at          TEXT        NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    updated_at          TEXT        NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE INDEX IF NOT EXISTS idx_playbooks_workspace_id ON playbooks (workspace_id);
CREATE INDEX IF NOT EXISTS idx_playbooks_confidence   ON playbooks (confidence DESC);

-- Mirrored from migrations/sqlite/000032_procedural_memories. Episodic/procedural
-- memory: reusable how-to knowledge with usage tracking.
-- No FK constraints (CLAUDE.md #9). Success count managed in application code.
CREATE TABLE IF NOT EXISTS procedural_memories (
    id            TEXT        PRIMARY KEY,
    workspace_id  TEXT,
    repo_name     TEXT        NOT NULL DEFAULT '',
    project_id    TEXT,
    title         TEXT        NOT NULL,
    when_to_use   TEXT        NOT NULL DEFAULT '',
    approach_md   TEXT        NOT NULL DEFAULT '',
    tools_used    TEXT        NOT NULL DEFAULT '[]',
    files_touched TEXT        NOT NULL DEFAULT '[]',
    success_count INTEGER     NOT NULL DEFAULT 0,
    last_used_at  TEXT,
    created_at    TEXT        NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE INDEX IF NOT EXISTS idx_procedural_memories_workspace ON procedural_memories(workspace_id);
CREATE INDEX IF NOT EXISTS idx_procedural_memories_repo      ON procedural_memories(repo_name);

-- Mirrored from migrations/sqlite/000033_memory_atoms. Atomic fact units extracted
-- from decisions, knowledge items, and procedural memories.
-- No FK constraints (CLAUDE.md #9). TTL: 90 days (enforced app-side via scheduled DELETE).
CREATE TABLE IF NOT EXISTS memory_atoms (
    id           TEXT        PRIMARY KEY DEFAULT (lower(hex(randomblob(4))) || '-' || lower(hex(randomblob(2))) || '-4' || substr(lower(hex(randomblob(2))),2) || '-' || substr('89ab', abs(random() % 4) + 1, 1) || substr(lower(hex(randomblob(2))),2) || '-' || lower(hex(randomblob(6)))),
    workspace_id TEXT,
    parent_table TEXT        NOT NULL,
    parent_id    TEXT        NOT NULL,
    content      TEXT        NOT NULL,
    keywords     TEXT        NOT NULL DEFAULT '[]',
    tags         TEXT        NOT NULL DEFAULT '[]',
    created_at   DATETIME    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
CREATE INDEX IF NOT EXISTS idx_memory_atoms_parent    ON memory_atoms(parent_table, parent_id);
CREATE INDEX IF NOT EXISTS idx_memory_atoms_workspace ON memory_atoms(workspace_id);

-- Mirrored from migrations/sqlite/000034_memory_links. Directed edges between atoms.
-- No FK constraints (CLAUDE.md #9).
CREATE TABLE IF NOT EXISTS memory_links (
    from_atom_id TEXT    NOT NULL,
    to_atom_id   TEXT    NOT NULL,
    link_type    TEXT    NOT NULL CHECK (link_type IN ('same_entity','same_action','same_time','same_project')),
    confidence   REAL    NOT NULL DEFAULT 0.5 CHECK (confidence BETWEEN 0.0 AND 1.0),
    created_at   DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    PRIMARY KEY (from_atom_id, to_atom_id, link_type)
);
CREATE INDEX IF NOT EXISTS idx_memory_links_from ON memory_links(from_atom_id);
CREATE INDEX IF NOT EXISTS idx_memory_links_to   ON memory_links(to_atom_id);

-- Mirrored from migrations/sqlite/000035_discipline_events. MCP tool-call audit
-- trail used by system_health drift detection (replacement for the cancelled
-- PreToolUse hook approach, decision e5310a15).
-- No FK constraints (CLAUDE.md #9). 30-day TTL enforced via scheduled DELETE
-- on Postgres; SQLite is dev-local single-tenant so growth is bounded.
CREATE TABLE IF NOT EXISTS discipline_events (
    id                 INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id         TEXT    NOT NULL,
    repo_name          TEXT,
    tool_name          TEXT    NOT NULL,
    is_mutating        INTEGER NOT NULL DEFAULT 0,
    observed_at        TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    linked_decision_id TEXT,
    workspace_id       TEXT
);
CREATE INDEX IF NOT EXISTS idx_discipline_events_observed_at
    ON discipline_events(observed_at DESC);
CREATE INDEX IF NOT EXISTS idx_discipline_events_workspace
    ON discipline_events(workspace_id, observed_at DESC) WHERE workspace_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_discipline_events_session_observed
    ON discipline_events(session_id, observed_at DESC);

-- Mirrored from migrations/sqlite/000042_completion_candidates.up.sql.
-- Completion candidate detection: tasks that appear done but GTD status is still open.
-- No FK constraints (CLAUDE.md #9). Referential integrity for task_id / workspace_id in code.
CREATE TABLE IF NOT EXISTS completion_candidates (
    id                 TEXT PRIMARY KEY,
    workspace_id       TEXT,
    task_id            TEXT NOT NULL,
    repo_name          TEXT,
    reason             TEXT NOT NULL,
    evidence_refs      TEXT NOT NULL DEFAULT '[]',
    confidence         TEXT NOT NULL CHECK (confidence IN ('high','medium','low')),
    suggested_artifact TEXT,
    status             TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','accepted','rejected','auto_applied')),
    detected_at        TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    resolved_at        TEXT
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_completion_candidates_task_reason
    ON completion_candidates(task_id, reason);
CREATE INDEX IF NOT EXISTS idx_completion_candidates_workspace_id
    ON completion_candidates(workspace_id) WHERE workspace_id IS NOT NULL;

-- Mirrored from migrations/sqlite/000052_merged_prs_observed.up.sql.
-- Persists observed merged PRs (Phase 2 of reconcile_merged_prs, GTD-fix
-- 10/12) so the daily fuzzy-match detector can correlate them against
-- pending tasks with null branch_name/pr_url linkage. 30-day TTL enforced
-- by the scheduler (backend-security-design.md §1.3). No FK per #9.
CREATE TABLE IF NOT EXISTS merged_prs_observed (
    id            TEXT PRIMARY KEY,
    workspace_id  TEXT,
    repo          TEXT NOT NULL,
    url           TEXT NOT NULL,
    head_ref      TEXT,
    title         TEXT,
    body_excerpt  TEXT,
    merged_at     TEXT NOT NULL,
    observed_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_merged_prs_observed_url
    ON merged_prs_observed(url);
CREATE INDEX IF NOT EXISTS idx_merged_prs_observed_observed_at_desc
    ON merged_prs_observed(observed_at DESC);
CREATE INDEX IF NOT EXISTS idx_merged_prs_observed_workspace_id
    ON merged_prs_observed(workspace_id) WHERE workspace_id IS NOT NULL;

-- Mirrored from migrations/sqlite/000047_task_pr_branch.up.sql.
CREATE INDEX IF NOT EXISTS idx_tasks_branch_name ON tasks(branch_name) WHERE branch_name IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_tasks_pr_url ON tasks(pr_url) WHERE pr_url IS NOT NULL;

-- Mirrored from migrations/sqlite/000045_composite_indexes.up.sql.
-- Composite indexes for frequent multi-column query patterns.
CREATE INDEX IF NOT EXISTS idx_tasks_workspace_status_priority
    ON tasks(workspace_id, status, priority DESC);
CREATE INDEX IF NOT EXISTS idx_tasks_workspace_project_updated
    ON tasks(workspace_id, project_id, updated_at DESC);
CREATE INDEX IF NOT EXISTS idx_decisions_workspace_repo_created
    ON decisions(workspace_id, repo_name, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_session_handoffs_workspace_repo_created
    ON session_handoffs(workspace_id, repo_name, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_activity_log_workspace_project_created
    ON activity_log(workspace_id, project_id, created_at DESC);
