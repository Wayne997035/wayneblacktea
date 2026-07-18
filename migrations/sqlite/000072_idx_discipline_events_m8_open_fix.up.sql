-- SQLite-only corrective migration for a pre-existing drift discovered while
-- building the golang-migrate runner (E1). migrations/sqlite/000059_discipline_events_m8.up.sql
-- (merged) creates idx_discipline_events_m8_open with a trailing
-- "created_at DESC"; internal/storage/sqlite/schema.sql (the runtime
-- authority until E1) has always declared it without DESC. Per
-- backend-security-design.md §6.4, merged migration files are immutable;
-- 000059 cannot be edited. Index rebuilds carry none of the risk a table
-- rebuild would (no data movement), so this is a straightforward DROP+CREATE
-- to align the final schema with the pre-refactor golden baseline exactly,
-- rather than adding a comparator allowlist entry for it.
DROP INDEX IF EXISTS idx_discipline_events_m8_open;
CREATE INDEX IF NOT EXISTS idx_discipline_events_m8_open
    ON discipline_events_m8 (workspace_id, created_at)
    WHERE resolved_at IS NULL;
