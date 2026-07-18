-- SQLite twin of migrations/000041_idx_tasks_due_date.up.sql.
-- This twin was missing entirely (numbering gap between 000040 and 000042);
-- internal/storage/sqlite/schema.sql never carried this index. Adding it here
-- is a net-new addition versus the pre-refactor schema, not a correction of
-- existing behaviour — see E1 dispatch notes.
CREATE INDEX IF NOT EXISTS idx_tasks_due_date ON tasks(due_date ASC) WHERE due_date IS NOT NULL;
