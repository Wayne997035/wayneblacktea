-- Reverse the backfill scaffold. Replace the sentinel UUID with whatever
-- value you used in the .up.sql before running.

\set BACKFILL_WORKSPACE_ID '''00000000-0000-0000-0000-000000000000'''

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
