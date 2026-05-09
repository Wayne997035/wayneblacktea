-- SQLite Migration 000026 rollback is intentionally a no-op.
--
-- Workspace policy (CLAUDE.md #9) forbids FOREIGN KEY / REFERENCES constraints
-- in any DB schema path — including rollback paths. The original rollback
-- content rebuilt each table WITH REFERENCES clauses, violating this red-line.
--
-- If you need to roll back past 000026, the database remains in the FK-free
-- state (correct policy-compliant state). Referential integrity is enforced
-- by the application layer.

SELECT 1;
