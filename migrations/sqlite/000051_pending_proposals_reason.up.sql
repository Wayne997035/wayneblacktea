-- SQLite parity for migration 000051 (pending_proposals.reason).
-- Free-text rejection reason populated by the scheduler's TTL prune job and
-- by future manual reject flows. No FOREIGN KEY per CLAUDE.md red-line §9.
ALTER TABLE pending_proposals ADD COLUMN reason TEXT;
