-- 000081_query_indexes.up.sql
-- [F0925-11] Existing-debt cleanup (PR #193 full-repo scan, decision
-- e0bfb453): 10 indexes for query paths that were already running against
-- these columns with no supporting index. Decision rule was "add only where
-- a real query path uses the column" — the other 14 flagged columns had no
-- query using them and are documented as exceptions on the scan side
-- instead of indexed here.
--
-- All 10 land here in Postgres. The SQLite twin
-- (migrations/sqlite/000081_query_indexes.up.sql) carries only 8: it skips
-- #2 (project_status_snapshots) because no SQLite store reads that table at
-- all, and #9 (guard_bypasses) because that table has no SQLite twin at
-- all — see the per-index comment below for the query path each one serves.
--
-- IF NOT EXISTS: safe to rerun. No CONCURRENTLY: golang-migrate applies each
-- migration inside a transaction, and CREATE INDEX CONCURRENTLY cannot run
-- inside one (https://www.postgresql.org/docs/current/sql-createindex.html).
-- No FK constraints (red line #9) — these are plain lookup/order-by indexes.

-- #1: DeleteProject's session_handoffs cleanup sweep
-- (internal/gtd/store.go: UPDATE session_handoffs SET project_id = NULL
-- WHERE project_id = $1).
CREATE INDEX IF NOT EXISTS idx_session_handoffs_project_id
    ON session_handoffs(project_id)
    WHERE project_id IS NOT NULL;

-- #2: LatestSlugs' workspace-scoped DISTINCT slug scan
-- (internal/snapshot/store.go).
CREATE INDEX IF NOT EXISTS idx_project_status_snapshots_workspace_slug
    ON project_status_snapshots(workspace_id, slug);

-- #3: DeleteProject's work_sessions.project_id cleanup sweep
-- (internal/gtd/store.go, same cleanup block as #1).
CREATE INDEX IF NOT EXISTS idx_work_sessions_project_id
    ON work_sessions(project_id)
    WHERE project_id IS NOT NULL;

-- #4: DeleteTask / DeleteProject's NullifyWorkSessionsCurrentTask sweep
-- (internal/gtd/store.go).
CREATE INDEX IF NOT EXISTS idx_work_sessions_current_task_id
    ON work_sessions(current_task_id)
    WHERE current_task_id IS NOT NULL;

-- #5: vision List's workspace-scoped scan ordered by created_at DESC.
CREATE INDEX IF NOT EXISTS idx_vision_items_workspace_created_at
    ON vision_items(workspace_id, created_at DESC);

-- #6: DeleteProject's vision_items.project_id cleanup sweep.
CREATE INDEX IF NOT EXISTS idx_vision_items_project_id
    ON vision_items(project_id)
    WHERE project_id IS NOT NULL;

-- #7: DeleteTask / DeleteProject's ResetPromotedVisionItems sweep
-- (internal/gtd/store.go).
CREATE INDEX IF NOT EXISTS idx_vision_items_promoted_task_id
    ON vision_items(promoted_task_id)
    WHERE promoted_task_id IS NOT NULL;

-- #8: DeleteProject's procedural_memories.project_id cleanup sweep.
CREATE INDEX IF NOT EXISTS idx_procedural_memories_project_id
    ON procedural_memories(project_id)
    WHERE project_id IS NOT NULL;

-- #9: the scheduler's daily guard_bypasses prune (DELETE ... WHERE
-- created_at < NOW() - 30d, internal/scheduler/scheduler.go) and
-- ListBypasses' ORDER BY created_at DESC (internal/guard/store.go).
-- PG only: guard_bypasses has no SQLite twin table.
CREATE INDEX IF NOT EXISTS idx_guard_bypasses_created_at
    ON guard_bypasses(created_at DESC);

-- #10: the decay pruner's memory_atoms range delete and the memory_links
-- from_atom_id/to_atom_id subqueries that precede it
-- (internal/atom/store.go, PruneAtoms).
CREATE INDEX IF NOT EXISTS idx_memory_atoms_created_at
    ON memory_atoms(created_at DESC);
