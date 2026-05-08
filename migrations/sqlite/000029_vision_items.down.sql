-- SQLite Migration 000029 rollback: remove vision_items table.
--
-- Plain SQL only; no psql metacommands.

DROP INDEX IF EXISTS idx_vision_items_initiative;
DROP INDEX IF EXISTS idx_vision_items_status;
DROP TABLE IF EXISTS vision_items;
