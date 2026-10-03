-- 000086_goals_active_due_date_idx.up.sql
-- [F1003-08] SQLite twin of migrations/000086_goals_active_due_date_idx.up.sql.
-- No "NULLS LAST"/"NULLS FIRST" keyword: SQLite's CREATE INDEX grammar does
-- not support null-ordering modifiers (https://www.sqlite.org/lang_createindex.html).
-- SQLite always treats NULL as smaller than all other values, so this plain
-- ascending index places NULL due_date rows first rather than last — a
-- real, accepted PG/SQLite divergence (see migration_000086_index_test.go).

CREATE INDEX IF NOT EXISTS idx_goals_active_due_date
    ON goals(workspace_id, due_date, id)
    WHERE status = 'active';
