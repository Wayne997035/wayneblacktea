CREATE TABLE IF NOT EXISTS completion_candidates (
    id                 TEXT PRIMARY KEY,
    workspace_id       TEXT,
    task_id            TEXT NOT NULL,
    repo_name          TEXT,
    reason             TEXT NOT NULL,
    evidence_refs      TEXT NOT NULL DEFAULT '[]',
    confidence         TEXT NOT NULL CHECK (confidence IN ('high','medium','low')),
    suggested_artifact TEXT,
    status             TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','accepted','rejected','auto_applied')),
    detected_at        TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    resolved_at        TEXT
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_completion_candidates_task_reason
    ON completion_candidates(task_id, reason);
CREATE INDEX IF NOT EXISTS idx_completion_candidates_workspace_id
    ON completion_candidates(workspace_id) WHERE workspace_id IS NOT NULL;
