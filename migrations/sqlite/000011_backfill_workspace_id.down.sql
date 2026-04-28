-- Reverse the SQLite backfill. Same sentinel-only safety guarantee as the
-- Postgres twin: only rows that match the sentinel UUID are flipped back
-- to NULL, so accidentally double-running this never nukes other workspaces.
--
-- Substitute the sentinel UUID with the same value used in the .up.sql
-- before running:
--   sed -i.bak "s/00000000-0000-0000-0000-000000000000/$WORKSPACE_UUID/g" \
--       migrations/sqlite/000011_backfill_workspace_id.down.sql
--   sqlite3 ./wayneblacktea.db < migrations/sqlite/000011_backfill_workspace_id.down.sql

UPDATE pending_proposals SET workspace_id = NULL WHERE workspace_id = '00000000-0000-0000-0000-000000000000';
UPDATE review_schedule   SET workspace_id = NULL WHERE workspace_id = '00000000-0000-0000-0000-000000000000';
UPDATE concepts          SET workspace_id = NULL WHERE workspace_id = '00000000-0000-0000-0000-000000000000';
UPDATE knowledge_items   SET workspace_id = NULL WHERE workspace_id = '00000000-0000-0000-0000-000000000000';
UPDATE session_handoffs  SET workspace_id = NULL WHERE workspace_id = '00000000-0000-0000-0000-000000000000';
UPDATE decisions         SET workspace_id = NULL WHERE workspace_id = '00000000-0000-0000-0000-000000000000';
UPDATE repos             SET workspace_id = NULL WHERE workspace_id = '00000000-0000-0000-0000-000000000000';
UPDATE activity_log      SET workspace_id = NULL WHERE workspace_id = '00000000-0000-0000-0000-000000000000';
UPDATE tasks             SET workspace_id = NULL WHERE workspace_id = '00000000-0000-0000-0000-000000000000';
UPDATE projects          SET workspace_id = NULL WHERE workspace_id = '00000000-0000-0000-0000-000000000000';
UPDATE goals             SET workspace_id = NULL WHERE workspace_id = '00000000-0000-0000-0000-000000000000';
