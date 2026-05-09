CREATE TABLE IF NOT EXISTS procedural_memories (
    id            TEXT PRIMARY KEY DEFAULT (lower(hex(randomblob(4))) || '-' || lower(hex(randomblob(2))) || '-4' || substr(lower(hex(randomblob(2))),2) || '-' || substr('89ab', abs(random() % 4) + 1, 1) || substr(lower(hex(randomblob(2))),2) || '-' || lower(hex(randomblob(6)))),
    workspace_id  TEXT,
    repo_name     TEXT NOT NULL DEFAULT '',
    project_id    TEXT,
    title         TEXT NOT NULL,
    when_to_use   TEXT NOT NULL DEFAULT '',
    approach_md   TEXT NOT NULL DEFAULT '',
    tools_used    TEXT NOT NULL DEFAULT '[]',
    files_touched TEXT NOT NULL DEFAULT '[]',
    success_count INTEGER NOT NULL DEFAULT 0,
    last_used_at  DATETIME,
    created_at    DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
