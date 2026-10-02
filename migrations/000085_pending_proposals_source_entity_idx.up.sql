-- 000085_pending_proposals_source_entity_idx.up.sql
-- [F0930-06] D10: supports the dedup NOT EXISTS lookup in
-- pgDecisionsPendingOutcomeReview (internal/scheduler/cognitive_jobs.go,
-- job 3) after [F0930-05] dropped its `AND p.status = 'pending'` predicate —
-- the candidate row set that predicate used to narrow (the type='task' +
-- status='pending' rows) widened to every type='task' row, and none of the
-- 3 existing pending_proposals indexes (000010_pending_proposals.up.sql:
-- idx_pending_proposals_status_pending, idx_pending_proposals_workspace_id,
-- idx_pending_proposals_type) cover proposed_by or the
-- payload->>'source_entity_id' expression the dedup predicate actually
-- filters on.
--
-- Column order (type, proposed_by, expression) matches the dedup query's
-- three equality predicates exactly (p.type = 'task' AND p.proposed_by =
-- 'scheduler:...' AND p.payload->>'source_entity_id' = d.id::text) — see
-- the migration_000085_index tests for the EXPLAIN proof this index is
-- actually used, not just present.
--
-- IF NOT EXISTS: safe to rerun. No FK constraints (red line #9).

CREATE INDEX IF NOT EXISTS idx_pending_proposals_type_proposer_source
    ON pending_proposals(type, proposed_by, (payload->>'source_entity_id'));
