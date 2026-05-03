-- Migration 000022: work_session_tasks join table
--
-- Links work_sessions to tasks with a role classification.
-- Referential integrity in code (red line #9; see migration 000026).
-- GTDStore.DeleteTask deletes link rows on parent task delete.
-- A future DeleteWorkSession handler MUST cleanup work_session_tasks rows
-- referencing session_id (mirrors GTDStore.DeleteTask precedent for tasks;
-- matches the SQLite-side schema.sql guidance).

CREATE TABLE IF NOT EXISTS work_session_tasks (
    -- Both columns: referential integrity in code (red line #9; see migration 000026).
    -- task_id cleanup happens in GTDStore.DeleteTask; session_id cleanup MUST be
    -- mirrored in a future DeleteWorkSession handler.
    session_id  UUID    NOT NULL,
    task_id     UUID    NOT NULL,
    role        TEXT    NOT NULL CHECK (role IN ('primary','follow_up','blocker','generated')),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (session_id, task_id)
);

CREATE INDEX idx_work_session_tasks_session_id
    ON work_session_tasks(session_id);

CREATE INDEX idx_work_session_tasks_task_id
    ON work_session_tasks(task_id);
