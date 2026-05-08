-- SQLite Migration 000029: create vision_items table.
--
-- SQLite differences vs the canonical Postgres schema:
--   * UUID columns are TEXT (canonical 8-4-4-4-12 lowercase). Generated app-side.
--   * TIMESTAMPTZ → TEXT in RFC3339 (UTC).
--   * JSONB depends_on → TEXT (json1 functions available since SQLite 3.9).
--   * gen_random_uuid() is not available; UUID generated in Go on insert.
--   * Partial indexes are supported since SQLite 3.8.9 (modernc.org/sqlite ships modern SQLite).
--
-- NOTE: The SQLite-backed runtime uses internal/storage/sqlite/schema.sql
-- directly (applied idempotently at sqlite.Open). This migration file exists
-- for Postgres↔SQLite numbering parity (backend-security-design.md §6.3) and
-- matches the vision_items table added to schema.sql.

CREATE TABLE IF NOT EXISTS vision_items (
    id                  TEXT    PRIMARY KEY,
    workspace_id        TEXT,
    repo_name           TEXT,
    project_id          TEXT,
    title               TEXT    NOT NULL,
    why_blocked         TEXT    NOT NULL,
    depends_on          TEXT    NOT NULL DEFAULT '[]',
    parent_initiative   TEXT,
    status              TEXT    NOT NULL DEFAULT 'open'
                            CHECK (status IN ('open','discussing','maturing','promoted','dismissed')),
    context_md          TEXT,
    promoted_task_id    TEXT,
    last_discussed_at   TEXT,
    created_at          TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

CREATE INDEX IF NOT EXISTS idx_vision_items_status
    ON vision_items(status) WHERE status != 'dismissed';

CREATE INDEX IF NOT EXISTS idx_vision_items_initiative
    ON vision_items(parent_initiative) WHERE parent_initiative IS NOT NULL;
