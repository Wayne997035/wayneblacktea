-- Migration 000022: work_session_tasks join table
--
-- Links work_sessions to tasks with a role classification.
-- ON DELETE CASCADE: removing a session or task removes the link.

CREATE TABLE IF NOT EXISTS work_session_tasks (
    session_id  UUID    NOT NULL REFERENCES work_sessions(id) ON DELETE CASCADE,
    task_id     UUID    NOT NULL REFERENCES tasks(id)         ON DELETE CASCADE,
    role        TEXT    NOT NULL CHECK (role IN ('primary','follow_up','blocker','generated')),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (session_id, task_id)
);

CREATE INDEX idx_work_session_tasks_session_id
    ON work_session_tasks(session_id);

CREATE INDEX idx_work_session_tasks_task_id
    ON work_session_tasks(task_id);
