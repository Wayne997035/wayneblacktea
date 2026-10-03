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
