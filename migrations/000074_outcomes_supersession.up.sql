-- 000074_outcomes_supersession.up.sql
-- Outcome lifecycle convergence (GTD decision 80c1e8ae, arch-r2 unit A13):
-- three call sites create outcomes (complete_task's seedDraftOutcome,
-- record_outcome, finish_work's autoCreateOutcomeOnFailure) and previously
-- had no rule preventing unrelated duplicate rows for the same logical
-- execution. Production audit found 2 entities with both an 'unknown' draft
-- and a terminal outcome coexisting — exactly the bug this migration + the
-- internal/outcome/lifecycle.go convergence rule closes.
--
-- 1. supersedes_id: when record_outcome/finish_work re-records a result for
--    an entity that already has a DIFFERENT terminal outcome, the new row
--    points back at the one it replaces instead of silently overwriting it
--    (audit trail preservation — see backend-security-design.md threat
--    model: a prompt-injected record_outcome call must not erase history).
--    NO FOREIGN KEY per CLAUDE.md red-line #9: supersedes_id references
--    another row in this same table; internal/outcome/lifecycle.go only ever
--    sets it to an id it just read via GetLatestForEntity, so referential
--    integrity is enforced in code, not by the database.
--
-- 2. idx_outcomes_one_open_draft: a partial unique index so complete_task's
--    seedDraftOutcome (internal/mcp/tools_gtd.go) can INSERT ... ON CONFLICT
--    DO NOTHING instead of the old racy ExistsForEntity-then-CreateOutcome
--    check-then-insert (TOCTOU under concurrent complete_task calls on the
--    same task). At most one result='unknown' row may exist per
--    (workspace, entity_type, entity_id) at a time; once finalized (result
--    changes away from 'unknown') the slot frees up for a future draft.
--    COALESCE(workspace_id, <nil-uuid>) is required because Postgres treats
--    every NULL as distinct in a unique index — without it, concurrent
--    unscoped (workspace_id IS NULL, legacy single-tenant mode) callers
--    would still race past this guard.
ALTER TABLE outcomes ADD COLUMN IF NOT EXISTS supersedes_id UUID NULL;

CREATE INDEX IF NOT EXISTS idx_outcomes_supersedes_id
    ON outcomes(supersedes_id) WHERE supersedes_id IS NOT NULL;

CREATE UNIQUE INDEX IF NOT EXISTS idx_outcomes_one_open_draft
    ON outcomes (COALESCE(workspace_id, '00000000-0000-0000-0000-000000000000'::uuid), entity_type, entity_id)
    WHERE result = 'unknown';
