-- 000063_outcomes_rule_link.down.sql (SQLite twin)
-- SQLite 3.35+ supports DROP COLUMN; drop related_rule_ids from outcomes.
-- The applyColumnUpgrades() idempotent path in db.go Open() does not run
-- this; down migrations are only applied via the migrate-down Taskfile target.
ALTER TABLE outcomes DROP COLUMN related_rule_ids;
