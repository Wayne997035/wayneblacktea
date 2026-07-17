-- Migration 000066: work_session_evidence table
--
-- wbt-2.0 Phase 2 "Action Lifecycle And Evidence Chain" (P2.1).
--
-- Stores individual evidence records (command output, PR link, CI result,
-- Railway deploy log, manual note) attached to a work session's finish_work
-- call. get_work_session_trace (P2.3) reads these rows to reconstruct what
-- was actually verified for a session.
--
-- NO FOREIGN KEY per CLAUDE.md red-line #9. workspace_id/session_id integrity
-- is enforced app-side (worksession.Store.AddEvidence/GetEvidence operate
-- within the same workspace scope as the rest of the worksession store).
--
-- Retention: 90-day TTL enforced by the daily-work-session-evidence-prune
-- scheduler job (internal/scheduler/scheduler.go WithWorkSessionEvidencePruner),
-- mirroring outcome.Store.PruneOlderThan (backend-security-design.md §1.3).
CREATE TABLE IF NOT EXISTS work_session_evidence (
    id             UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id   UUID        NULL,
    session_id     UUID        NOT NULL,
    evidence_type  TEXT        NOT NULL CHECK (evidence_type IN ('command','pr','ci','railway','manual_note')),
    status         TEXT        NOT NULL CHECK (status IN ('passed','failed','unknown')),
    command        TEXT        NULL,
    artifact       TEXT        NULL,
    output_excerpt TEXT        NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_work_session_evidence_session_id
    ON work_session_evidence(session_id);

CREATE INDEX IF NOT EXISTS idx_work_session_evidence_workspace_session_created
    ON work_session_evidence(workspace_id, session_id, created_at DESC);
