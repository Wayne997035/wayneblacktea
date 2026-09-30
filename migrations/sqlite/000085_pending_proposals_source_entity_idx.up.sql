-- 000085_pending_proposals_source_entity_idx.up.sql (SQLite twin)
-- [F0930-06] SQLite twin of migrations/000085_pending_proposals_source_entity_idx.up.sql.
-- Same column order (type, proposed_by, expression). The expression uses
-- json_extract instead of Postgres's ->>'source_entity_id' operator —
-- pending_proposals.payload is stored as TEXT JSON in SQLite
-- (internal/storage/sqlite/cognitive_jobs.go's DecisionsPendingOutcomeReview
-- already uses the same json_extract shape in its dedup NOT EXISTS
-- predicate; SQLite's query planner only matches an expression index when
-- the query expression is byte-identical to the index expression).
--
-- WHERE type = 'task' (unlike the PG twin, which has no WHERE clause): a
-- non-partial expression index forces SQLite to evaluate
-- json_extract(payload, ...) on every INSERT/UPDATE to pending_proposals to
-- maintain the index, and json_extract raises "malformed JSON" for a
-- syntactically invalid payload — even for rows the dedup query never reads.
-- This repo has existing negative-path tests that deliberately store
-- syntactically-invalid JSON for type='knowledge'/type='decision' proposals
-- to exercise the confirm-time json.Unmarshal failure path
-- (internal/mcp/tools_proposal_knowledge_test.go's
-- TestConfirmProposal_TypeKnowledge_BadPayload,
-- internal/mcp/tools_proposal_decision_batch_test.go's
-- TestHandleConfirmProposals_BadDecisionPayload_FailsThatIDOnly) — without
-- this predicate, migration 000085 would make those inserts fail at the
-- SQLite layer, breaking coverage of a real production code path. Postgres
-- needs no equivalent restriction: payload there is JSONB, which already
-- rejects syntactically invalid JSON at the column-type level regardless of
-- any index, so the PG twin never had this failure mode. `type = 'task'` is
-- exactly what pgDecisionsPendingOutcomeReview /
-- DecisionsPendingOutcomeReview always filter on, so this loses no coverage
-- for the query this index exists to serve — and it also transparently
-- serves any other type='task' scheduler job doing the same
-- (type, proposed_by, source_entity_id) dedup shape (e.g. job 2's
-- stuck_task_detection), not just decision_outcome_review.
--
-- IF NOT EXISTS: safe to rerun. No FK constraints (red line #9).

CREATE INDEX IF NOT EXISTS idx_pending_proposals_type_proposer_source
    ON pending_proposals(type, proposed_by, json_extract(payload, '$.source_entity_id'))
    WHERE type = 'task';
