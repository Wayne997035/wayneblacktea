CREATE TABLE decisions (
    id           UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    project_id   UUID, -- referential integrity in code (red line #9; see migration 000026)
    repo_name    TEXT,
    title        TEXT NOT NULL,
    context      TEXT NOT NULL,
    decision     TEXT NOT NULL,
    rationale    TEXT NOT NULL,
    alternatives TEXT,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_decisions_project_id ON decisions(project_id);
CREATE INDEX idx_decisions_repo_name ON decisions(repo_name);
CREATE INDEX idx_decisions_created_at ON decisions(created_at DESC);
