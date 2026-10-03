-- 000087_pending_proposals_status_pending_sort_key.up.sql
-- [F1003-09] SQLite twin of migrations/000087_pending_proposals_status_pending_sort_key.up.sql.
-- Identical column list and predicate — no NULLS LAST divergence to
-- document here (created_at and id are both non-nullable sort keys).

DROP INDEX IF EXISTS idx_pending_proposals_status_pending;
CREATE INDEX IF NOT EXISTS idx_pending_proposals_status_pending
    ON pending_proposals(created_at DESC, id DESC)
    WHERE status = 'pending';
