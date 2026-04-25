CREATE TABLE repos (
    id                UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    name              TEXT NOT NULL UNIQUE,
    path              TEXT,
    description       TEXT,
    language          TEXT,
    status            TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','archived','on_hold')),
    current_branch    TEXT,
    known_issues      TEXT[],
    next_planned_step TEXT,
    last_activity     TIMESTAMPTZ,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_repos_status ON repos(status);
