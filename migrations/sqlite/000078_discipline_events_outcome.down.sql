-- 000078_discipline_events_outcome.down.sql (SQLite twin)
-- [F184-04]
--
-- SQLite 3.35+ supports DROP COLUMN (see 000076's SQLite down twin for the
-- same note — not a concern on this repo's target platforms). Lossy for
-- error_class / response_bytes / duration_ms and for `ok` on any row where
-- it is actually false — see the Postgres down twin's comment.
ALTER TABLE discipline_events DROP COLUMN duration_ms;
ALTER TABLE discipline_events DROP COLUMN response_bytes;
ALTER TABLE discipline_events DROP COLUMN error_class;
ALTER TABLE discipline_events DROP COLUMN ok;
