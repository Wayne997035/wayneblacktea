-- SQLite-dialect twin of migrations/000011_backfill_workspace_id.up.sql.
--
-- The SQLite backend has no incremental migration runner — schema.sql is
-- applied idempotently at sqlite.New() time. This file is therefore a
-- one-shot script run via `sqlite3 <db-path> < this-file` on the rare
-- occasion a SQLite self-hoster wants to backfill workspace scoping.
--
-- Usage (mirrors the Postgres flow in docs/operations.md):
--   1. Pick a UUID:    WORKSPACE_UUID=$(uuidgen | tr '[:upper:]' '[:lower:]')
--   2. Substitute:     sed -i.bak "s/00000000-0000-0000-0000-000000000000/$WORKSPACE_UUID/g" \
--                          migrations/sqlite/000011_backfill_workspace_id.up.sql \
--                          migrations/sqlite/000011_backfill_workspace_id.down.sql
--   3. Apply:          sqlite3 ./wayneblacktea.db < migrations/sqlite/000011_backfill_workspace_id.up.sql
--   4. Set WORKSPACE_ID in your .env, restart the server.
--
-- Differences vs Postgres twin: workspace_id columns are TEXT (not UUID),
-- so we compare against the literal string. There are no `\set` psql
-- variables in SQLite, so the sentinel is inlined in each statement and
-- replaced via sed.

UPDATE goals             SET workspace_id = '00000000-0000-0000-0000-000000000000' WHERE workspace_id IS NULL;
UPDATE projects          SET workspace_id = '00000000-0000-0000-0000-000000000000' WHERE workspace_id IS NULL;
UPDATE tasks             SET workspace_id = '00000000-0000-0000-0000-000000000000' WHERE workspace_id IS NULL;
UPDATE activity_log      SET workspace_id = '00000000-0000-0000-0000-000000000000' WHERE workspace_id IS NULL;
UPDATE repos             SET workspace_id = '00000000-0000-0000-0000-000000000000' WHERE workspace_id IS NULL;
UPDATE decisions         SET workspace_id = '00000000-0000-0000-0000-000000000000' WHERE workspace_id IS NULL;
UPDATE session_handoffs  SET workspace_id = '00000000-0000-0000-0000-000000000000' WHERE workspace_id IS NULL;
UPDATE knowledge_items   SET workspace_id = '00000000-0000-0000-0000-000000000000' WHERE workspace_id IS NULL;
UPDATE concepts          SET workspace_id = '00000000-0000-0000-0000-000000000000' WHERE workspace_id IS NULL;
UPDATE review_schedule   SET workspace_id = '00000000-0000-0000-0000-000000000000' WHERE workspace_id IS NULL;
UPDATE pending_proposals SET workspace_id = '00000000-0000-0000-0000-000000000000' WHERE workspace_id IS NULL;
