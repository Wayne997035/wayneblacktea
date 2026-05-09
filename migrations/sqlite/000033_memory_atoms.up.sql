CREATE TABLE IF NOT EXISTS memory_atoms (
    id           TEXT PRIMARY KEY DEFAULT (lower(hex(randomblob(4))) || '-' || lower(hex(randomblob(2))) || '-4' || substr(lower(hex(randomblob(2))),2) || '-' || substr('89ab', abs(random() % 4) + 1, 1) || substr(lower(hex(randomblob(2))),2) || '-' || lower(hex(randomblob(6)))),
    workspace_id TEXT,
    parent_table TEXT NOT NULL,
    parent_id    TEXT NOT NULL,
    content      TEXT NOT NULL,
    keywords     TEXT NOT NULL DEFAULT '[]',
    tags         TEXT NOT NULL DEFAULT '[]',
    created_at   DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
CREATE INDEX IF NOT EXISTS idx_memory_atoms_parent ON memory_atoms(parent_table, parent_id);
CREATE INDEX IF NOT EXISTS idx_memory_atoms_workspace ON memory_atoms(workspace_id);
