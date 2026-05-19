-- Migration 000051: add reason column to pending_proposals.
-- Records WHY a proposal was rejected so the scheduler's daily TTL-prune
-- (30 days for status='pending' AND type='task') can mark the row with
-- reason='ttl-expired-30d' instead of silently dropping it, and so the
-- proposal UI can show "rejected because…" instead of unattributed cleanup.
-- Backend-security-design.md §1.3 (observability table TTL) + GTD 947f3f12.
-- NO FOREIGN KEY (CLAUDE.md red-line §9; reason is free-text metadata).
ALTER TABLE pending_proposals ADD COLUMN IF NOT EXISTS reason TEXT;
