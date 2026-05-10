-- Adds 'decision' to the pending_proposals.type allowlist so the
-- auto-decision-proposer middleware (internal/mcp/middleware_decision_proposer.go)
-- can route LLM-drafted decision proposals through the existing pending_proposals
-- queue and confirm_proposal materialise switches.
--
-- Round-trip flow:
--   middleware writes pending_proposals row of type='decision' with
--   proposal.DecisionProposerPayload JSON →
--   user accepts via /api/proposals/:id/confirm or confirm_proposal MCP tool →
--   materialiser inserts a `decisions` row + marks the proposal accepted.
--
-- No FK constraint is added (per CLAUDE.md red-line #9 +
-- backend-security-design.md §1.1) — the decisions row references nothing on
-- the proposal side after materialisation.
ALTER TABLE pending_proposals
    DROP CONSTRAINT IF EXISTS pending_proposals_type_check;

ALTER TABLE pending_proposals
    ADD CONSTRAINT pending_proposals_type_check
    CHECK (type IN ('goal','project','task','concept','knowledge','playbook','decision'));
