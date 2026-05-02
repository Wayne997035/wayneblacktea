-- Revert: remove 'knowledge' from pending_proposals.type CHECK constraint.
ALTER TABLE pending_proposals
    DROP CONSTRAINT IF EXISTS pending_proposals_type_check;

ALTER TABLE pending_proposals
    ADD CONSTRAINT pending_proposals_type_check
    CHECK (type IN ('goal','project','task','concept'));
