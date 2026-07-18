-- SQLite-only corrective migration for a pre-existing gap discovered while
-- building the golang-migrate runner (E1). migrations/sqlite/000030_playbooks.up.sql
-- is a merged no-op stub ("SELECT 1; -- covered by schema.sql") that never
-- actually created the playbooks table — internal/storage/sqlite/schema.sql
-- carries the real CREATE TABLE directly and was the sole runtime authority
-- until this refactor. Per backend-security-design.md §6.4, merged migration
-- files are immutable; 000030 cannot be edited, so this file supplies the DDL
-- 000030 should have contained. No corresponding PG migration is needed (PG's
-- own 000030_playbooks.up.sql already creates the table correctly).
--
-- No FOREIGN KEY per CLAUDE.md red-line #9.
CREATE TABLE IF NOT EXISTS playbooks (
    id                  TEXT        PRIMARY KEY,
    workspace_id        TEXT,
    trigger_pattern     TEXT        NOT NULL,
    action_template     TEXT        NOT NULL,
    source_decision_ids TEXT        NOT NULL DEFAULT '[]',
    confidence          REAL        NOT NULL DEFAULT 0.50,
    hits                INTEGER     NOT NULL DEFAULT 0,
    last_used_at        TEXT,
    created_at          TEXT        NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    updated_at          TEXT        NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE INDEX IF NOT EXISTS idx_playbooks_workspace_id ON playbooks (workspace_id);
CREATE INDEX IF NOT EXISTS idx_playbooks_confidence   ON playbooks (confidence DESC);
