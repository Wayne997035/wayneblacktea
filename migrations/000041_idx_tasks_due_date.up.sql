-- 000041: partial index on tasks.due_date for upcoming-tasks query performance.
-- Covers pending/in_progress tasks with a due date, ordered ASC.
-- Retention: permanent (structural index, not observability table — no TTL needed).
CREATE INDEX IF NOT EXISTS idx_tasks_due_date ON tasks(due_date ASC) WHERE due_date IS NOT NULL;
