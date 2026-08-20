-- 000076_decision_actor_provenance.down.sql (SQLite twin)
--
-- SQLite 3.35+ supports DROP COLUMN (see 000073's SQLite down twin for the
-- same note re: older SQLite versions — not a concern on this repo's target
-- platforms). Provenance-lossy — see the Postgres down twin's comment.
ALTER TABLE decisions DROP COLUMN confirmed_by_human;
ALTER TABLE decisions DROP COLUMN actor_session_id;
