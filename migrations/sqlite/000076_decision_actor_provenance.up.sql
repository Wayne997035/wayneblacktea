-- 000076_decision_actor_provenance.up.sql (SQLite twin)
--
-- See migrations/000076_decision_actor_provenance.up.sql for full design
-- rationale. SQLite has no native BOOLEAN type — confirmed_by_human is
-- INTEGER NOT NULL DEFAULT 0, mirroring the existing is_mutating / would_deny
-- convention already used in this repo (migrations/sqlite/000035_discipline_
-- events.up.sql; internal/storage/sqlite/discipline.go stores 0/1 and
-- converts to/from Go bool at the store boundary — the same conversion
-- pattern applies to confirmed_by_human in internal/storage/sqlite/
-- decision.go). SQLite's ALTER TABLE ADD COLUMN has no IF NOT EXISTS form, so
-- a re-run already fails loudly (same note as 000073's SQLite twin).

ALTER TABLE decisions ADD COLUMN actor_session_id TEXT;
ALTER TABLE decisions ADD COLUMN confirmed_by_human INTEGER NOT NULL DEFAULT 0;
