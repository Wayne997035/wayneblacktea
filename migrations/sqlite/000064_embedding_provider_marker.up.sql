-- 000064_embedding_provider_marker.up.sql (SQLite twin)
--
-- Adds embedding provider metadata columns to session_handoffs, decisions,
-- and project_status_snapshots for SQLite databases.
--
-- SQLite 3.35+ supports ALTER TABLE ... ADD COLUMN.
-- All three columns are nullable with no DEFAULT (back-compatible; existing
-- rows keep NULL and are treated as 'hashed' by SearchByCosine).
--
-- The idempotent applyColumnUpgrades() path in db.go Open() issues the same
-- ALTER TABLE statements on startup, ignoring "duplicate column name" errors.
-- This migration file records the intent for reviewers following §6.3 dual-backend
-- parity rule.
--
-- No FOREIGN KEY per CLAUDE.md red-line #9.

ALTER TABLE session_handoffs         ADD COLUMN embedding_provider TEXT;
ALTER TABLE session_handoffs         ADD COLUMN embedding_model     TEXT;
ALTER TABLE session_handoffs         ADD COLUMN embedding_dim       INTEGER;

ALTER TABLE decisions                ADD COLUMN embedding_provider TEXT;
ALTER TABLE decisions                ADD COLUMN embedding_model     TEXT;
ALTER TABLE decisions                ADD COLUMN embedding_dim       INTEGER;

ALTER TABLE project_status_snapshots ADD COLUMN embedding_provider TEXT;
ALTER TABLE project_status_snapshots ADD COLUMN embedding_model     TEXT;
ALTER TABLE project_status_snapshots ADD COLUMN embedding_dim       INTEGER;

-- Tag all existing non-null-embedding rows as 'hashed'/32-dim.
--
-- decisions is deliberately excluded here (see migrations/HISTORICAL_EXCEPTIONS.md):
-- unlike session_handoffs and project_status_snapshots, decisions has never
-- carried a raw `embedding` column in internal/storage/sqlite/schema.sql (the
-- runtime authority until E1) — migration 000020 added it, but migration
-- 000026's FK-drop table-rebuild dropped it again (decisions_new's column
-- list never included it), and no SQLite DB has ever actually executed this
-- file before E1 made migrations/sqlite/ a real runner. Backfilling a column
-- that no real schema state has would error "no such column: embedding".
UPDATE session_handoffs
   SET embedding_provider = 'hashed', embedding_dim = 32
 WHERE embedding IS NOT NULL AND embedding_provider IS NULL;

UPDATE project_status_snapshots
   SET embedding_provider = 'hashed', embedding_dim = 32
 WHERE embedding IS NOT NULL AND embedding_provider IS NULL;
