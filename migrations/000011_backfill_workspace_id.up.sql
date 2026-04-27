-- Phase B2 follow-up: backfill scaffold for assigning a workspace_id to all
-- pre-existing rows in one shot.
--
-- THIS MIGRATION IS NOT AUTO-RUN. It is intentionally a scaffold because:
--   * The target workspace UUID must be your own — pick one and put it in
--     the WORKSPACE_ID env (e.g. uuidgen | tr A-Z a-z) before running.
--   * Once applied, rows that previously had NULL workspace_id are bound to
--     that workspace. Setting WORKSPACE_ID to a different UUID will hide
--     them again.
--
-- To use:
--   1. Generate a UUID and remember it (set in your .env as WORKSPACE_ID).
--   2. Replace the literal '00000000-0000-0000-0000-000000000000' below
--      with that UUID.
--   3. Run this file manually:
--        psql "$DATABASE_URL" -f migrations/000011_backfill_workspace_id.up.sql
--   4. Set WORKSPACE_ID in .env, restart the MCP / server process.
--
-- The down migration sets every row back to NULL workspace_id only for rows
-- that match the placeholder UUID, so accidental re-runs do not nuke other
-- workspaces' data.

-- BACKFILL_WORKSPACE_ID is a sentinel — replace before applying.
\set BACKFILL_WORKSPACE_ID '''00000000-0000-0000-0000-000000000000'''

UPDATE goals             SET workspace_id = :BACKFILL_WORKSPACE_ID::uuid WHERE workspace_id IS NULL;
UPDATE projects          SET workspace_id = :BACKFILL_WORKSPACE_ID::uuid WHERE workspace_id IS NULL;
UPDATE tasks             SET workspace_id = :BACKFILL_WORKSPACE_ID::uuid WHERE workspace_id IS NULL;
UPDATE activity_log      SET workspace_id = :BACKFILL_WORKSPACE_ID::uuid WHERE workspace_id IS NULL;
UPDATE repos             SET workspace_id = :BACKFILL_WORKSPACE_ID::uuid WHERE workspace_id IS NULL;
UPDATE decisions         SET workspace_id = :BACKFILL_WORKSPACE_ID::uuid WHERE workspace_id IS NULL;
UPDATE session_handoffs  SET workspace_id = :BACKFILL_WORKSPACE_ID::uuid WHERE workspace_id IS NULL;
UPDATE knowledge_items   SET workspace_id = :BACKFILL_WORKSPACE_ID::uuid WHERE workspace_id IS NULL;
UPDATE concepts          SET workspace_id = :BACKFILL_WORKSPACE_ID::uuid WHERE workspace_id IS NULL;
UPDATE review_schedule   SET workspace_id = :BACKFILL_WORKSPACE_ID::uuid WHERE workspace_id IS NULL;
UPDATE pending_proposals SET workspace_id = :BACKFILL_WORKSPACE_ID::uuid WHERE workspace_id IS NULL;
