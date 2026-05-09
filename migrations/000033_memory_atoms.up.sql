CREATE TABLE IF NOT EXISTS memory_atoms (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID,
    parent_table TEXT NOT NULL,          -- 'decisions' | 'knowledge_items' | 'procedural_memories'
    parent_id    UUID NOT NULL,
    content      TEXT NOT NULL,
    keywords     JSONB NOT NULL DEFAULT '[]',
    tags         JSONB NOT NULL DEFAULT '[]',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_memory_atoms_parent ON memory_atoms(parent_table, parent_id);
CREATE INDEX IF NOT EXISTS idx_memory_atoms_workspace ON memory_atoms(workspace_id);
-- TTL enforcement: atoms older than 90 days are stale; document this
COMMENT ON TABLE memory_atoms IS 'Atomic fact units; retain 90 days (TTL not enforced by DB; use scheduled DELETE)';
