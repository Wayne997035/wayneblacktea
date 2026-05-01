-- SQLite-dialect twin of migrations/000015_session_summary_embedding.down.sql.
--
-- SQLite does not support DROP COLUMN on tables with indexes or triggers.
-- The canonical rollback strategy is to recreate the table without the columns.
-- This script is provided for reference; in practice the schema.sql applied at
-- sqlite.New() already includes both columns as nullable, so they are harmless
-- if the operator does not run this script.

CREATE TABLE session_handoffs_old AS
    SELECT id, workspace_id, project_id, repo_name, intent, context_summary,
           resolved_at, created_at
    FROM session_handoffs;

DROP TABLE session_handoffs;

ALTER TABLE session_handoffs_old RENAME TO session_handoffs;

CREATE INDEX IF NOT EXISTS idx_session_handoffs_unresolved
    ON session_handoffs(created_at DESC) WHERE resolved_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_session_handoffs_workspace_id
    ON session_handoffs(workspace_id) WHERE workspace_id IS NOT NULL;
