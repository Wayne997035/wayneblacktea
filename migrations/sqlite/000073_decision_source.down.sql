-- 000073_decision_source.down.sql (SQLite twin)
--
-- SQLite 3.35+ supports DROP COLUMN (see 000064's down twin for the same
-- note re: older SQLite versions — not a concern on this repo's target
-- platforms). Provenance-lossy — see the Postgres down twin's comment for
-- the recomputation-on-re-up caveat; identical here.
ALTER TABLE decisions DROP COLUMN source;
