-- Extend pending_proposals.type CHECK constraint to allow 'knowledge'.
-- knowledge proposals are written by the reflection/consolidation cron jobs
-- and follow the same pending→accepted/rejected lifecycle as other types.
-- The user confirms via GET /proposals/pending + POST /proposals/:id/confirm.

ALTER TABLE pending_proposals
    DROP CONSTRAINT IF EXISTS pending_proposals_type_check;

ALTER TABLE pending_proposals
    ADD CONSTRAINT pending_proposals_type_check
    CHECK (type IN ('goal','project','task','concept','knowledge'));
