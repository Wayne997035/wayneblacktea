-- 000063_outcomes_rule_link.down.sql
ALTER TABLE outcomes DROP COLUMN IF EXISTS related_rule_ids;
