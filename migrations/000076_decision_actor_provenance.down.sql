-- 000076_decision_actor_provenance.down.sql
-- Plain SQL only — no psql metacommands (backend-security-design.md §6.1).
--
-- Drops both columns. Provenance-lossy for actor_session_id and
-- confirmed_by_human, same shape as 000073_decision_source's down — neither
-- column has an application writer as of this migration (contract layer
-- only), so there is no live data to lose in practice at the time this ships.
ALTER TABLE decisions DROP COLUMN IF EXISTS confirmed_by_human;
ALTER TABLE decisions DROP COLUMN IF EXISTS actor_session_id;
