-- SQLite parity for migration 000039: no-op.
-- The canonical SQLite schema (internal/storage/sqlite/schema.sql) does NOT
-- pre-bind repo_name on any project — local SQLite users always start with
-- NULL repo_name and the workspace overview handler falls through to the
-- legacy ProjectByName lookup. Production backfill is Postgres-only.
SELECT 1;
