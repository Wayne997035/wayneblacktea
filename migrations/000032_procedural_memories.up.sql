CREATE TABLE IF NOT EXISTS procedural_memories (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id   UUID,
    repo_name      TEXT NOT NULL DEFAULT '',
    project_id     UUID,
    title          TEXT NOT NULL,
    when_to_use    TEXT NOT NULL DEFAULT '',
    approach_md    TEXT NOT NULL DEFAULT '',
    tools_used     JSONB NOT NULL DEFAULT '[]',
    files_touched  JSONB NOT NULL DEFAULT '[]',
    success_count  INT NOT NULL DEFAULT 0,
    last_used_at   TIMESTAMPTZ,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_procedural_memories_workspace ON procedural_memories(workspace_id);
CREATE INDEX IF NOT EXISTS idx_procedural_memories_repo ON procedural_memories(repo_name);
