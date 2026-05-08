-- Migration 000029 rollback: remove vision_items table and its indexes.
--
-- Plain SQL only; no psql metacommands (\set, :variable, \copy, etc.) —
-- golang-migrate cannot parse them (backend-security-design.md §6.1).

DROP INDEX IF EXISTS idx_vision_items_initiative;
DROP INDEX IF EXISTS idx_vision_items_status;
DROP TABLE IF EXISTS vision_items;
