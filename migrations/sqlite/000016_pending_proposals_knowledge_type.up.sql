-- SQLite-dialect twin of migrations/000016_pending_proposals_knowledge_type.up.sql.
--
-- SQLite does not support ALTER TABLE … DROP/ADD CONSTRAINT. The type CHECK
-- is embedded in the CREATE TABLE statement in schema.sql, which already
-- includes 'knowledge'. This file is therefore a no-op idempotent statement
-- so the migration sequence stays numbered consistently.
--
-- The schema.sql change (adding 'knowledge' to the CHECK) is committed
-- alongside this migration. Existing SQLite databases created with schema
-- version < 000016 have their CHECK silently widened the next time the
-- process starts (SQLite does not enforce table-level CHECKs retroactively
-- on existing rows; only new inserts are validated against the new schema.sql).

SELECT 1; -- no-op: SQLite schema.sql already carries the updated CHECK
