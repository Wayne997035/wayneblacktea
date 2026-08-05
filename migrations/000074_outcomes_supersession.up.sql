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
--    (audit trail preservation). A terminal outcome row is never rewritten
--    in place — the only in-place UPDATE this table permits is
--    internal/outcome/lifecycle.go's FinalizeDraft transitioning a
--    result='unknown' draft to its first terminal result (guarded by
--    `WHERE result = 'unknown'`, so it can only ever fire once per row).
--    See backend-security-design.md's threat model on audit-trail loss and
--    PR #152 finding M-1 (record_outcome(result='unknown') against an
--    existing draft is a no-op, not a way to erase draft content — closed
--    at the internal/outcome/lifecycle.go layer, not in SQL).
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
--    COALESCE(workspace_id, <nil-uuid>) is required because this is standard
--    SQL NULL semantics — every NULL is distinct from every other NULL in a
--    unique index/constraint, on both Postgres and SQLite — without it,
--    concurrent unscoped (workspace_id IS NULL, legacy single-tenant mode)
--    callers would still race past this guard.
--
--    Pre-existing installs can already have >1 result='unknown' row per
--    group (the old code path was an unconditional INSERT with no
--    uniqueness rule at all — see PR #152 finding M-2), which would abort
--    CREATE UNIQUE INDEX mid-migration. The DELETE below dedups first,
--    keeping the most-recently-created 'unknown' row per group. This is a
--    behavior no-op, not just storage cleanup: GetLatestForEntity (and every
--    call path built on it — RecordExecutionResult, SeedDraft, FinalizeDraft)
--    already `ORDER BY created_at DESC LIMIT 1`, so any *older* duplicate
--    'unknown' row for the same entity was already unreachable dead weight
--    before this migration ran — no application code path has ever read it
--    except full-listing views (list_recent_outcomes, find_failed_patterns).
--    Evaluations pointing at a row being deleted are removed first (no FK
--    cascade per CLAUDE.md red-line #9; mirrors internal/outcome/store.go's
--    PruneOlderThan delete-evaluations-first pattern) so no evaluation is
--    left pointing at a deleted outcome.
ALTER TABLE outcomes ADD COLUMN IF NOT EXISTS supersedes_id UUID NULL;

DELETE FROM evaluations
WHERE outcome_id IN (
    SELECT id FROM (
        SELECT id, ROW_NUMBER() OVER (
            PARTITION BY COALESCE(workspace_id, '00000000-0000-0000-0000-000000000000'::uuid), entity_type, entity_id
            ORDER BY created_at DESC, id DESC
        ) AS rn
        FROM outcomes
        WHERE result = 'unknown'
    ) ranked
    WHERE rn > 1
);

DELETE FROM outcomes
WHERE id IN (
    SELECT id FROM (
        SELECT id, ROW_NUMBER() OVER (
            PARTITION BY COALESCE(workspace_id, '00000000-0000-0000-0000-000000000000'::uuid), entity_type, entity_id
            ORDER BY created_at DESC, id DESC
        ) AS rn
        FROM outcomes
        WHERE result = 'unknown'
    ) ranked
    WHERE rn > 1
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_outcomes_one_open_draft
    ON outcomes (COALESCE(workspace_id, '00000000-0000-0000-0000-000000000000'::uuid), entity_type, entity_id)
    WHERE result = 'unknown';
