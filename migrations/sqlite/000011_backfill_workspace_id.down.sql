-- Reverse the SQLite backfill. Same sentinel-only safety guarantee as the
-- Postgres twin: only rows that match the sentinel UUID are flipped back
-- to NULL, so accidentally double-running this never nukes other workspaces.
--
-- Usage: see docs/operations.md → "Rollback (if you picked the wrong UUID)"
-- under the SQLite self-hosters section. The runbook uses a /tmp/ copy
-- + sed flow; do NOT sed in place against this committed file.

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
