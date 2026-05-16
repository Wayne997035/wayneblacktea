CREATE TABLE completion_candidates (
    id                 UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id       UUID,
    task_id            UUID        NOT NULL,
    repo_name          TEXT,
    reason             TEXT        NOT NULL,
    evidence_refs      TEXT[]      NOT NULL DEFAULT '{}',
    confidence         TEXT        NOT NULL CHECK (confidence IN ('high','medium','low')),
    suggested_artifact TEXT,
    status             TEXT        NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','accepted','rejected','auto_applied')),
    detected_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    resolved_at        TIMESTAMPTZ
);

CREATE UNIQUE INDEX idx_completion_candidates_task_reason
    ON completion_candidates(task_id, reason);
CREATE INDEX idx_completion_candidates_workspace_id
    ON completion_candidates(workspace_id) WHERE workspace_id IS NOT NULL;
CREATE INDEX idx_completion_candidates_status_pending
    ON completion_candidates(detected_at DESC) WHERE status = 'pending';
