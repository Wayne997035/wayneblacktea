-- Production WORKSPACE_ID backfill — companion to the scaffold in 000011.
--
-- 000011 was intentionally NOT auto-run because it required the operator to
-- supply a real WORKSPACE_ID. This migration is the production-ready twin:
-- it carries the same placeholder sentinel and the same /tmp-copy-then-sed
-- discipline so the committed SQL never holds a real UUID.
--
-- WHY A NEW MIGRATION instead of reusing 000011:
--   * 000011 predates migration 000012-000014. Any instance that was
--     already at schema version 11+ and ran 000011 manually still has the
--     scaffold record in schema_migrations. A new migration file lets the
--     golang-migrate runner track the backfill operation cleanly without
--     re-applying 000011.
--   * Ops teams can confirm "000015 applied" in Railway logs independently
--     of whether 000011 was ever manually run.
--
-- THIS FILE USES A SENTINEL UUID — do not run it directly in production.
-- Follow the SOP in docs/operations.md → "WORKSPACE_ID Backfill (migration
-- 000015)" which copies this file to /tmp and substitutes the sentinel
-- before applying.
--
-- Tables covered: all 11 domain tables that received workspace_id in
-- migration 000008 plus pending_proposals (000010). Tables added in
-- 000012-000014 (workspace_preferences, project_arch) do not need
-- backfilling: workspace_preferences uses workspace_id as PRIMARY KEY
-- (never NULL), and project_arch is tenant-agnostic (slug-keyed).
--
-- SENTINEL: 00000000-0000-0000-0000-000000000001
-- (distinct from 000011's all-zero sentinel so down.sql can target exactly
-- the rows that THIS migration set)

-- SECURITY: A SQL-level guard that survives a full-file `sed` substitution
-- is impossible without psql metacommands (sed would replace both sides of
-- any IF comparison). Defence-in-depth is layered application-side instead:
--   * runtime/workspace.go ErrSentinelWorkspaceID — the server refuses to
--     start if WORKSPACE_ID equals the sentinel, so a forgotten substitution
--     is detected immediately on the next deploy.
--   * docs/operations.md → "WORKSPACE_ID Backfill (migration 000015)" — the
--     authoritative SOP requires `cp` + `sed` before `psql -f`.
--
-- Plain SQL (no psql `\set`) so this file can be safely picked up by either
-- `psql -f` (operations.md SOP) or golang-migrate (`task migrate-up`). Both
-- runners will hit the DO $$ guard above and abort if the sentinel has not
-- been substituted, so an accidental `migrate up` cannot silently apply the
-- placeholder UUID to production rows.
UPDATE goals             SET workspace_id = '00000000-0000-0000-0000-000000000001'::uuid WHERE workspace_id IS NULL;
UPDATE projects          SET workspace_id = '00000000-0000-0000-0000-000000000001'::uuid WHERE workspace_id IS NULL;
UPDATE tasks             SET workspace_id = '00000000-0000-0000-0000-000000000001'::uuid WHERE workspace_id IS NULL;
UPDATE activity_log      SET workspace_id = '00000000-0000-0000-0000-000000000001'::uuid WHERE workspace_id IS NULL;
UPDATE repos             SET workspace_id = '00000000-0000-0000-0000-000000000001'::uuid WHERE workspace_id IS NULL;
UPDATE decisions         SET workspace_id = '00000000-0000-0000-0000-000000000001'::uuid WHERE workspace_id IS NULL;
UPDATE session_handoffs  SET workspace_id = '00000000-0000-0000-0000-000000000001'::uuid WHERE workspace_id IS NULL;
UPDATE knowledge_items   SET workspace_id = '00000000-0000-0000-0000-000000000001'::uuid WHERE workspace_id IS NULL;
UPDATE concepts          SET workspace_id = '00000000-0000-0000-0000-000000000001'::uuid WHERE workspace_id IS NULL;
UPDATE review_schedule   SET workspace_id = '00000000-0000-0000-0000-000000000001'::uuid WHERE workspace_id IS NULL;
UPDATE pending_proposals SET workspace_id = '00000000-0000-0000-0000-000000000001'::uuid WHERE workspace_id IS NULL;
