-- Migration 000056 (SQLite): Skill Library — reusable Claude Code skill definitions.
-- TEXT columns mirror the Postgres JSONB/UUID/TIMESTAMPTZ columns.
-- UUID default expression copied from memory_atoms in internal/storage/sqlite/schema.sql.
-- No FOREIGN KEY constraints (CLAUDE.md red-line §9).
CREATE TABLE IF NOT EXISTS skills (
    id                     TEXT        PRIMARY KEY DEFAULT (lower(hex(randomblob(4))) || '-' || lower(hex(randomblob(2))) || '-4' || substr(lower(hex(randomblob(2))),2) || '-' || substr('89ab', abs(random() % 4) + 1, 1) || substr(lower(hex(randomblob(2))),2) || '-' || lower(hex(randomblob(6)))),
    workspace_id           TEXT,
    name                   TEXT        NOT NULL,
    description            TEXT        NOT NULL DEFAULT '',
    triggers               TEXT        NOT NULL DEFAULT '[]',
    steps                  TEXT        NOT NULL DEFAULT '[]',
    failure_modes          TEXT        NOT NULL DEFAULT '[]',
    verification_checklist TEXT        NOT NULL DEFAULT '[]',
    examples               TEXT        NOT NULL DEFAULT '[]',
    source_atom_ids        TEXT        NOT NULL DEFAULT '[]',
    success_count          INTEGER     NOT NULL DEFAULT 0,
    failure_count          INTEGER     NOT NULL DEFAULT 0,
    last_used_at           TEXT,
    created_at             TEXT        NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    updated_at             TEXT        NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE INDEX IF NOT EXISTS idx_skills_workspace  ON skills(workspace_id);
CREATE INDEX IF NOT EXISTS idx_skills_name       ON skills(name);
CREATE INDEX IF NOT EXISTS idx_skills_success    ON skills(workspace_id, success_count DESC);
CREATE INDEX IF NOT EXISTS idx_skills_last_used  ON skills(last_used_at DESC);
