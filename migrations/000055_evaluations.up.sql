-- Migration 000055: evaluations table records structured analysis attached to
-- an outcome. Each evaluation captures root-cause analysis, lessons learned,
-- and improvement suggestions to feed the reflection loop.
--
-- Retention: 90 days (same as outcomes). Pruned via outcomes FK-free cascade
-- in code: the prune job deletes evaluations first (WHERE outcome_id IN (...))
-- then outcomes. Per backend-security-design.md §1.3.
--
-- No FOREIGN KEY constraints (CLAUDE.md red-line §9): outcome_id is a logical
-- reference enforced in code (GetOutcomeByID before CreateEvaluation).
CREATE TABLE evaluations (
    id                       UUID        PRIMARY KEY DEFAULT uuid_generate_v4(),
    workspace_id             UUID,
    outcome_id               UUID        NOT NULL,
    analysis                 TEXT        NOT NULL,
    lessons                  JSONB       NOT NULL DEFAULT '[]',
    improvement_suggestions  JSONB       NOT NULL DEFAULT '[]',
    created_at               TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_evaluations_outcome_id ON evaluations(outcome_id);
CREATE INDEX idx_evaluations_workspace_id ON evaluations(workspace_id) WHERE workspace_id IS NOT NULL;
CREATE INDEX idx_evaluations_created_at ON evaluations(created_at DESC);
