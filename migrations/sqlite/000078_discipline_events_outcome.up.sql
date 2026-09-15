-- 000078_discipline_events_outcome.up.sql (SQLite twin)
-- [F184-04]
--
-- See migrations/000078_discipline_events_outcome.up.sql for full design
-- rationale. SQLite has no native BOOLEAN type — ok is INTEGER NOT NULL
-- DEFAULT 1, mirroring the existing is_mutating 0/1 convention already used
-- by this table (migrations/sqlite/000035_discipline_events.up.sql;
-- internal/storage/sqlite/discipline.go converts to/from Go bool at the
-- store boundary — the same conversion pattern applies to `ok`).

ALTER TABLE discipline_events ADD COLUMN ok INTEGER NOT NULL DEFAULT 1;
ALTER TABLE discipline_events ADD COLUMN error_class TEXT;
ALTER TABLE discipline_events ADD COLUMN response_bytes INTEGER;
ALTER TABLE discipline_events ADD COLUMN duration_ms INTEGER;
