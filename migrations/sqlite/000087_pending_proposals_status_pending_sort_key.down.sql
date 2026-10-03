-- 000087_pending_proposals_status_pending_sort_key.down.sql
-- [F1003-09]

DROP INDEX IF EXISTS idx_pending_proposals_status_pending;
CREATE INDEX IF NOT EXISTS idx_pending_proposals_status_pending
    ON pending_proposals(created_at DESC)
    WHERE status = 'pending';
