-- 000087_pending_proposals_status_pending_sort_key.up.sql
-- [F1003-09] Realigns idx_pending_proposals_status_pending to carry the
-- full sort key ListPendingProposals (sql/queries/proposal.sql) actually
-- orders by — created_at DESC, id DESC — not just created_at DESC. Without
-- the id tiebreaker, rows sharing a created_at timestamp have no stable
-- order, which under LIMIT/OFFSET pagination can drop or repeat rows
-- across pages (same failure mode documented at sql/queries/gtd.sql).
--
-- DROP before CREATE: CREATE INDEX IF NOT EXISTS only checks the name, and
-- idx_pending_proposals_status_pending already exists from migration 000010
-- — same reasoning as migrations/sqlite/000082_index_parity.up.sql.
-- No CONCURRENTLY — golang-migrate applies each migration inside a
-- transaction and CREATE INDEX CONCURRENTLY cannot run inside one (same
-- citation as migrations/000081_query_indexes.up.sql). No FK (red line #9).

DROP INDEX IF EXISTS idx_pending_proposals_status_pending;
CREATE INDEX IF NOT EXISTS idx_pending_proposals_status_pending
    ON pending_proposals(created_at DESC, id DESC)
    WHERE status = 'pending';

-- [F1003-09] Decision 7a064608 (post-STOP follow-up): the above index
-- keeps serving the workspace_id-NULL global path and SQLite's
-- single-tenant mode, but production is NOT assumed single-workspace —
-- measured (internal/db/migration_000087_explain_test.go, 2000+2000-row
-- scale) that at LIMIT 500 the planner abandons it for
-- idx_pending_proposals_workspace_id + an explicit Sort, because
-- workspace_id isn't in the index at all. This net-new, workspace_id-
-- leading composite gives the per-workspace production path (every real
-- call binds a concrete workspace_id — internal/storage/factory.go
-- fail-closes Postgres startup otherwise) an Index Cond on workspace_id
-- AND the exact (created_at, id) trailing order, same equality-then-sort
-- shape as idx_goals_active_due_date (migrations/000086). No FK (red line
-- #9); IF NOT EXISTS is safe here since this name is net-new, not a
-- realignment.
CREATE INDEX IF NOT EXISTS idx_pending_proposals_workspace_pending_sort
    ON pending_proposals(workspace_id, created_at DESC, id DESC)
    WHERE status = 'pending';
