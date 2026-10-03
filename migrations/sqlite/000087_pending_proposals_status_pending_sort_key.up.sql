-- 000087_pending_proposals_status_pending_sort_key.up.sql
-- [F1003-09] SQLite twin of migrations/000087_pending_proposals_status_pending_sort_key.up.sql.
-- Identical column list and predicate — no NULLS LAST divergence to
-- document here (created_at and id are both non-nullable sort keys).

DROP INDEX IF EXISTS idx_pending_proposals_status_pending;
CREATE INDEX IF NOT EXISTS idx_pending_proposals_status_pending
    ON pending_proposals(created_at DESC, id DESC)
    WHERE status = 'pending';

-- [F1003-09] SQLite twin of the workspace-leading composite added to
-- migrations/000087_*.up.sql. Not required for SQLite's own
-- single-tenant-by-default usage, but kept for dual-backend parity — same
-- column list and predicate as PG, no NULLS LAST divergence to document
-- here.
CREATE INDEX IF NOT EXISTS idx_pending_proposals_workspace_pending_sort
    ON pending_proposals(workspace_id, created_at DESC, id DESC)
    WHERE status = 'pending';
