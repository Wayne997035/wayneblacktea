-- Drop FK before dropping the table it references.
ALTER TABLE guard_events DROP CONSTRAINT IF EXISTS fk_guard_events_bypass;

DROP TABLE IF EXISTS guard_bypasses;
