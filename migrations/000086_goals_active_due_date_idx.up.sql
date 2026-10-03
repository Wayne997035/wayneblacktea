-- 000086_goals_active_due_date_idx.up.sql
-- [F1003-08] ListActiveGoals (sql/queries/gtd.sql) / ActiveGoalsPage
-- (internal/storage/sqlite/gtd.go) filter WHERE status = 'active' and order
-- BY due_date ASC NULLS LAST, id ASC, with no supporting index on `goals` —
-- every call was a full table scan plus an in-memory sort.
--
-- workspace_id leads the composite: every production call binds a concrete
-- workspace_id (internal/storage/factory.go fail-closes Postgres startup
-- when WORKSPACE_ID is unset), so this is the equality-then-sort column
-- order already used by idx_vision_items_workspace_created_at
-- (migrations/000081_query_indexes.up.sql). The workspace_id-NULL branch
-- (SQLite local-dev-only legacy path) can still use this index for the
-- status='active' partial predicate but may need an extra Sort — an
-- accepted, documented trade-off (see migration_000086 EXPLAIN tests), not
-- a production regression.
--
-- IF NOT EXISTS: safe to rerun. No CONCURRENTLY: golang-migrate applies
-- each migration inside a transaction, and CREATE INDEX CONCURRENTLY
-- cannot run inside one (same citation as
-- migrations/000081_query_indexes.up.sql). No FK constraints (red line #9).

CREATE INDEX IF NOT EXISTS idx_goals_active_due_date
    ON goals(workspace_id, due_date ASC NULLS LAST, id ASC)
    WHERE status = 'active';
