CREATE TABLE IF NOT EXISTS memory_links (
    from_atom_id TEXT NOT NULL,
    to_atom_id   TEXT NOT NULL,
    link_type    TEXT NOT NULL CHECK (link_type IN ('same_entity','same_action','same_time','same_project')),
    confidence   REAL NOT NULL DEFAULT 0.5 CHECK (confidence BETWEEN 0.0 AND 1.0),
    created_at   DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    PRIMARY KEY (from_atom_id, to_atom_id, link_type)
);
CREATE INDEX IF NOT EXISTS idx_memory_links_from ON memory_links(from_atom_id);
CREATE INDEX IF NOT EXISTS idx_memory_links_to ON memory_links(to_atom_id);
