-- 000080_deletion_tombstones.up.sql (SQLite twin)
-- [F191-03]
--
-- See migrations/000080_deletion_tombstones.up.sql for the full design
-- rationale. Dialect differences from the Postgres original:
--   * UUID -> TEXT. Every id / foreign-reference column in this backend
--     already stores a UUID string in TEXT (e.g. vision_items.id — see
--     migrations/sqlite/000029_vision_items.up.sql).
--   * JSONB -> TEXT. SQLite has no native JSON type; payload is a JSON
--     string built by json_object() at the call site, the same convention
--     every other JSON-shaped column in this backend already uses.
--   * TIMESTAMPTZ -> TEXT, no strftime() DEFAULT — deleted_at is supplied by
--     the Go orchestration layer on every insert, exactly as the Postgres
--     twin's column has no DEFAULT NOW() either (see that file's column
--     notes on why a per-statement default cannot be used here).
--   * CREATE TABLE (not IF NOT EXISTS): a re-run MUST fail loudly rather
--     than silently no-op, matching 000079's convention.

CREATE TABLE deletion_tombstones (
    id           TEXT PRIMARY KEY,
    workspace_id TEXT,
    deletion_id  TEXT NOT NULL,
    entity_kind  TEXT NOT NULL CHECK (entity_kind IN ('project', 'task')),
    entity_id    TEXT NOT NULL,
    project_id   TEXT,
    payload      TEXT NOT NULL,
    deleted_by   TEXT NOT NULL,
    deleted_at   TEXT NOT NULL
);

CREATE INDEX idx_deletion_tombstones_project_deleted_at
    ON deletion_tombstones(project_id, deleted_at DESC);

CREATE INDEX idx_deletion_tombstones_deleted_at
    ON deletion_tombstones(deleted_at);

CREATE INDEX idx_deletion_tombstones_deletion_id
    ON deletion_tombstones(deletion_id);
