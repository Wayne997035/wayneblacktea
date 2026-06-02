-- 000064_embedding_provider_marker.down.sql
-- Plain SQL only — no psql metacommands (golang-migrate §6.1).
ALTER TABLE session_handoffs         DROP COLUMN IF EXISTS embedding_provider;
ALTER TABLE session_handoffs         DROP COLUMN IF EXISTS embedding_model;
ALTER TABLE session_handoffs         DROP COLUMN IF EXISTS embedding_dim;

ALTER TABLE decisions                DROP COLUMN IF EXISTS embedding_provider;
ALTER TABLE decisions                DROP COLUMN IF EXISTS embedding_model;
ALTER TABLE decisions                DROP COLUMN IF EXISTS embedding_dim;

ALTER TABLE project_status_snapshots DROP COLUMN IF EXISTS embedding_provider;
ALTER TABLE project_status_snapshots DROP COLUMN IF EXISTS embedding_model;
ALTER TABLE project_status_snapshots DROP COLUMN IF EXISTS embedding_dim;
