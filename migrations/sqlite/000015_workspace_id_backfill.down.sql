-- SQLite-dialect twin of migrations/000015_workspace_id_backfill.down.sql.
--
-- Substitute the sentinel with the real UUID you used before running.
-- See docs/operations.md for the /tmp-copy + sed procedure.
--
-- Sentinel: 00000000-0000-0000-0000-000000000001

UPDATE pending_proposals SET workspace_id = NULL WHERE workspace_id = '00000000-0000-0000-0000-000000000001';
UPDATE review_schedule   SET workspace_id = NULL WHERE workspace_id = '00000000-0000-0000-0000-000000000001';
UPDATE concepts          SET workspace_id = NULL WHERE workspace_id = '00000000-0000-0000-0000-000000000001';
UPDATE knowledge_items   SET workspace_id = NULL WHERE workspace_id = '00000000-0000-0000-0000-000000000001';
UPDATE session_handoffs  SET workspace_id = NULL WHERE workspace_id = '00000000-0000-0000-0000-000000000001';
UPDATE decisions         SET workspace_id = NULL WHERE workspace_id = '00000000-0000-0000-0000-000000000001';
UPDATE repos             SET workspace_id = NULL WHERE workspace_id = '00000000-0000-0000-0000-000000000001';
UPDATE activity_log      SET workspace_id = NULL WHERE workspace_id = '00000000-0000-0000-0000-000000000001';
UPDATE tasks             SET workspace_id = NULL WHERE workspace_id = '00000000-0000-0000-0000-000000000001';
UPDATE projects          SET workspace_id = NULL WHERE workspace_id = '00000000-0000-0000-0000-000000000001';
UPDATE goals             SET workspace_id = NULL WHERE workspace_id = '00000000-0000-0000-0000-000000000001';
