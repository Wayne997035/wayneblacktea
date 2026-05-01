-- Reverse the production backfill applied by 000015.
--
-- Only rows whose workspace_id matches the sentinel
-- 00000000-0000-0000-0000-000000000001 are reverted. If the operator
-- substituted their real UUID before applying 000015.up.sql, they MUST
-- apply the same substitution here before running the down migration:
--
--   cp migrations/000015_workspace_id_backfill.down.sql /tmp/applied-000015.down.sql
--   sed -i "s/00000000-0000-0000-0000-000000000001/$WORKSPACE_UUID/g" \
--       /tmp/applied-000015.down.sql
--   psql "$DATABASE_URL" -f /tmp/applied-000015.down.sql
--
-- See docs/operations.md → "WORKSPACE_ID Backfill (migration 000015)" →
-- "Rollback" for the full procedure.

\set BACKFILL_WORKSPACE_ID '''00000000-0000-0000-0000-000000000001'''

UPDATE pending_proposals SET workspace_id = NULL WHERE workspace_id = :BACKFILL_WORKSPACE_ID::uuid;
UPDATE review_schedule   SET workspace_id = NULL WHERE workspace_id = :BACKFILL_WORKSPACE_ID::uuid;
UPDATE concepts          SET workspace_id = NULL WHERE workspace_id = :BACKFILL_WORKSPACE_ID::uuid;
UPDATE knowledge_items   SET workspace_id = NULL WHERE workspace_id = :BACKFILL_WORKSPACE_ID::uuid;
UPDATE session_handoffs  SET workspace_id = NULL WHERE workspace_id = :BACKFILL_WORKSPACE_ID::uuid;
UPDATE decisions         SET workspace_id = NULL WHERE workspace_id = :BACKFILL_WORKSPACE_ID::uuid;
UPDATE repos             SET workspace_id = NULL WHERE workspace_id = :BACKFILL_WORKSPACE_ID::uuid;
UPDATE activity_log      SET workspace_id = NULL WHERE workspace_id = :BACKFILL_WORKSPACE_ID::uuid;
UPDATE tasks             SET workspace_id = NULL WHERE workspace_id = :BACKFILL_WORKSPACE_ID::uuid;
UPDATE projects          SET workspace_id = NULL WHERE workspace_id = :BACKFILL_WORKSPACE_ID::uuid;
UPDATE goals             SET workspace_id = NULL WHERE workspace_id = :BACKFILL_WORKSPACE_ID::uuid;
