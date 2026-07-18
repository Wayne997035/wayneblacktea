-- SQLite-only corrective migration for a pre-existing gap discovered while
-- building the golang-migrate runner (E1). migrations/sqlite/000061_ai_cost_ledger.up.sql
-- is a merged no-op stub ("SELECT 1; -- no-op: SQLite dev path uses NopRecorder;
-- no table required.") that never created the ai_cost_ledger table — but
-- internal/storage/sqlite/schema.sql DOES carry the table ("present for schema
-- completeness but never written to in local dev"), so schema.sql and its own
-- twin migration disagree. Per backend-security-design.md §6.4, merged migration
-- files are immutable; 000061 cannot be edited, so this file supplies the DDL
-- needed to keep the SQLite runner's end state matching the pre-refactor
-- schema.sql (golden-equivalence requirement). No PG twin needed (PG's own
-- 000061_ai_cost_ledger.up.sql already creates the table correctly).
--
-- The table is never written to by the SQLite backend (pgRecorder-only path);
-- it exists purely for schema parity/completeness. No FOREIGN KEY per
-- CLAUDE.md red-line #9.
CREATE TABLE IF NOT EXISTS ai_cost_ledger (
    id                 TEXT    PRIMARY KEY,
    workspace_id       TEXT,
    caller             TEXT    NOT NULL,
    model              TEXT    NOT NULL,
    input_tokens       INTEGER NOT NULL DEFAULT 0,
    output_tokens      INTEGER NOT NULL DEFAULT 0,
    cache_read_tokens  INTEGER NOT NULL DEFAULT 0,
    cache_write_tokens INTEGER NOT NULL DEFAULT 0,
    cost_usd_micro     INTEGER NOT NULL DEFAULT 0,
    created_at         TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
CREATE INDEX IF NOT EXISTS idx_ai_cost_ledger_workspace_time
    ON ai_cost_ledger (workspace_id, created_at DESC);
