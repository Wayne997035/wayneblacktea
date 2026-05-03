-- SQLite Migration 000022: work_session_tasks join table
-- Referential integrity in code (red line #9; see migration 000026).
-- GTDStore.DeleteTask deletes link rows on parent task delete.

CREATE TABLE IF NOT EXISTS work_session_tasks (
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
