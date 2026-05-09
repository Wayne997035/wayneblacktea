-- Migration 000026 rollback is intentionally a no-op.
--
-- Workspace policy (CLAUDE.md #9) forbids FOREIGN KEY constraints in any DB
-- schema path — including rollback paths. The original rollback content
-- re-added FK constraints, which violates this red-line regardless of
-- migration direction.
--
-- If you need to roll back past 000026, the database remains in the FK-free
-- state (which is the correct, policy-compliant state). Referential integrity
-- is enforced by the application layer (service/handler validate-on-write +
-- nullable JOIN on read).

SELECT 1;
