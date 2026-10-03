-- 000087_pending_proposals_status_pending_sort_key.down.sql
-- [F1003-09] Restores the pre-087 shape (migrations/000010_pending_proposals.up.sql).
-- Decision 7a064608: also drops the net-new workspace-leading index added
-- by this same migration's up.sql.

DROP INDEX IF EXISTS idx_pending_proposals_workspace_pending_sort;
DROP INDEX IF EXISTS idx_pending_proposals_status_pending;
CREATE INDEX IF NOT EXISTS idx_pending_proposals_status_pending
    ON pending_proposals(created_at DESC)
    WHERE status = 'pending';
