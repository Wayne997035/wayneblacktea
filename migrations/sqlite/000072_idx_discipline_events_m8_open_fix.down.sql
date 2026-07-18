DROP INDEX IF EXISTS idx_discipline_events_m8_open;
CREATE INDEX IF NOT EXISTS idx_discipline_events_m8_open
    ON discipline_events_m8 (workspace_id, created_at DESC)
    WHERE resolved_at IS NULL;
