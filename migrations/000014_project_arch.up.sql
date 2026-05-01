-- Architecture snapshot store.
-- Lets Claude persist a project's file map and summary so it doesn't
-- re-read the same internal/ files every session.

CREATE TABLE project_arch (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    slug            TEXT NOT NULL UNIQUE,
    summary         TEXT NOT NULL,
    file_map        JSONB NOT NULL DEFAULT '{}',
    last_commit_sha TEXT NOT NULL DEFAULT '',
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_project_arch_slug ON project_arch(slug);
