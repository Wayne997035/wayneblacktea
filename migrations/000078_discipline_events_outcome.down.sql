-- 000078_discipline_events_outcome.down.sql
-- [F184-04] Plain SQL only — no psql metacommands (backend-security-design.md §6.1).
--
-- Drops all four columns. Lossy for error_class / response_bytes /
-- duration_ms (and for `ok` on any row where it is actually false) — same
-- shape as 000076's down migration.
ALTER TABLE discipline_events DROP COLUMN IF EXISTS duration_ms;
ALTER TABLE discipline_events DROP COLUMN IF EXISTS response_bytes;
ALTER TABLE discipline_events DROP COLUMN IF EXISTS error_class;
ALTER TABLE discipline_events DROP COLUMN IF EXISTS ok;
