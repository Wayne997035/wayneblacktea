-- Migration 000068: work session evidence chain — index hardening
--
-- wbt-2.0 P2 review fixes (F3/F4):
--   F3: PruneOlderThan (worksession.Store.PruneOlderThan) deletes
--       work_session_evidence WHERE created_at < $1 with no usable index —
--       the existing indexes are (session_id) and (workspace_id, session_id,
--       created_at DESC); neither has created_at as a leading column, so the
--       daily prune job (internal/scheduler/scheduler.go
--       WithWorkSessionEvidencePruner) full-table-scans. Add a dedicated
--       index.
--   F4: work_sessions.outcome_id and outcomes.work_session_id (added in
--       000065/000067) back get_work_session_trace / record_outcome reverse
--       lookups but have no index. Add both as partial indexes (most rows
--       have these columns NULL — auto-outcome creation only fires on
--       failure/partial/regressed, and record_outcome's session_id is
--       optional), matching the idx_work_sessions_workspace_id /
--       idx_outcomes_workspace_entity partial-index convention already used
--       in this schema.
--
-- context_pack_id (also added in 000065) is intentionally left unindexed:
-- intentionally unindexed: unpopulated in Phase 2
-- (internal/mcp/tools_worksession.go:198 — start_work does not persist
-- assemble_context Packs yet), so there is no query path to serve.
--
-- NO FOREIGN KEY per CLAUDE.md red-line #9 — these are plain indexes only.

CREATE INDEX IF NOT EXISTS idx_work_session_evidence_created_at
    ON work_session_evidence(created_at);

CREATE INDEX IF NOT EXISTS idx_work_sessions_outcome_id
    ON work_sessions(outcome_id) WHERE outcome_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_outcomes_work_session_id
    ON outcomes(work_session_id) WHERE work_session_id IS NOT NULL;
