-- 000064_embedding_provider_marker.down.sql (SQLite twin)
--
-- SQLite 3.35+ supports DROP COLUMN; removes the embedding provider metadata
-- columns added by the corresponding .up.sql migration.
--
-- Note: SQLite <3.35 does not support DROP COLUMN. On older SQLite versions
-- the correct approach is to recreate the table via the recommended 12-step
-- rename-create-insert-drop procedure. Since wayneblacktea targets modern
-- platforms (macOS 12+, which ships SQLite >=3.37) this is not a concern
-- in practice, but operators on older SQLite should apply down migrations
-- manually if needed.
--
-- The applyColumnUpgrades() path in db.go Open() does not apply down migrations;
-- they are only used via the migrate-down Taskfile target.

ALTER TABLE session_handoffs         DROP COLUMN embedding_provider;
ALTER TABLE session_handoffs         DROP COLUMN embedding_model;
ALTER TABLE session_handoffs         DROP COLUMN embedding_dim;

ALTER TABLE decisions                DROP COLUMN embedding_provider;
ALTER TABLE decisions                DROP COLUMN embedding_model;
ALTER TABLE decisions                DROP COLUMN embedding_dim;

ALTER TABLE project_status_snapshots DROP COLUMN embedding_provider;
ALTER TABLE project_status_snapshots DROP COLUMN embedding_model;
ALTER TABLE project_status_snapshots DROP COLUMN embedding_dim;
