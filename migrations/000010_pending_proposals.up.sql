-- Phase A: proposal gate infrastructure.
-- Stores AI-agent proposals for high-commitment entities (goals, projects,
-- tasks, concepts) that the user must explicitly accept before they become
-- real. Phase B will add MCP tools (propose_*, confirm_proposal) on top.

CREATE TABLE pending_proposals (
    id            UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    workspace_id  UUID,
    type          TEXT NOT NULL CHECK (type IN ('goal','project','task','concept')),
    payload       JSONB NOT NULL,
    status        TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','accepted','rejected')),
    proposed_by   TEXT,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    resolved_at   TIMESTAMPTZ
);

CREATE INDEX idx_pending_proposals_status_pending ON pending_proposals(created_at DESC) WHERE status = 'pending';
CREATE INDEX idx_pending_proposals_workspace_id   ON pending_proposals(workspace_id) WHERE workspace_id IS NOT NULL;
CREATE INDEX idx_pending_proposals_type           ON pending_proposals(type);
