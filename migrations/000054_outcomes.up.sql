-- Migration 000054: outcomes table records the result of a task, decision,
-- sprint, or project execution. Closes the Action→Result loop by letting the
-- system track what actually happened after a plan was executed.
--
-- Retention: 90 days. Enforced by the scheduler's daily 03:30 prune job
-- (DELETE WHERE created_at < NOW() - INTERVAL '90 days') per
-- backend-security-design.md §1.3 — observability tables MUST have a
-- working retention policy in the same PR that introduces them.
--
-- No FOREIGN KEY constraints (CLAUDE.md red-line §9): entity_id references
-- tasks, decisions, sprints, or projects depending on entity_type; referential
-- integrity is enforced in code at the service/handler layer.
CREATE TABLE outcomes (
    id           UUID        PRIMARY KEY DEFAULT uuid_generate_v4(),
    workspace_id UUID,
    entity_type  TEXT        NOT NULL,
    entity_id    UUID        NOT NULL,
    result       TEXT        NOT NULL CHECK (result IN ('success','failure','partial','unknown','regressed')),
    metrics      JSONB,
    notes        TEXT,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_outcomes_workspace_entity ON outcomes(workspace_id, entity_type, entity_id);
CREATE INDEX idx_outcomes_entity_id ON outcomes(entity_id);
CREATE INDEX idx_outcomes_result ON outcomes(result);
CREATE INDEX idx_outcomes_created_at ON outcomes(created_at DESC);
