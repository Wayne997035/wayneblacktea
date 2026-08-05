-- 000074_outcomes_supersession.up.sql (SQLite twin)
-- See migrations/000074_outcomes_supersession.up.sql for the Postgres source
-- of truth and full design rationale (GTD decision 80c1e8ae, arch-r2 A13),
-- including the M-1/M-2 findings from PR #152's second army referenced
-- below. The dedup DELETEs and index below are semantically equivalent to
-- the Postgres file (same ROW_NUMBER() OVER partitioning, same tie-break).
--
-- Dialect differences: UUID -> TEXT. Partial + expression unique indexes are
-- supported by SQLite since 3.8/3.9 (modernc.org/sqlite v1.50.0 bundles a
-- far newer SQLite), including as an ON CONFLICT target for UPSERT
-- (SQLite 3.24+), which internal/storage/sqlite/outcome.go's SeedDraft
-- relies on. Window functions (ROW_NUMBER() OVER) are supported since
-- SQLite 3.25 (2018) — used here instead of GROUP BY + MAX(created_at) so
-- exactly one row per group is kept even when two rows share the same
-- created_at (SQLite's TEXT timestamp has millisecond granularity and ties
-- are easy to hit in fast test loops); the `id DESC` tie-break makes the
-- kept row deterministic.
--
-- No FOREIGN KEY per CLAUDE.md red-line #9.
ALTER TABLE outcomes ADD COLUMN supersedes_id TEXT;

DELETE FROM evaluations
WHERE outcome_id IN (
    SELECT id FROM (
        SELECT id, ROW_NUMBER() OVER (
            PARTITION BY COALESCE(workspace_id, '00000000-0000-0000-0000-000000000000'), entity_type, entity_id
            ORDER BY created_at DESC, id DESC
        ) AS rn
        FROM outcomes
        WHERE result = 'unknown'
    )
    WHERE rn > 1
);

DELETE FROM outcomes
WHERE id IN (
    SELECT id FROM (
        SELECT id, ROW_NUMBER() OVER (
            PARTITION BY COALESCE(workspace_id, '00000000-0000-0000-0000-000000000000'), entity_type, entity_id
            ORDER BY created_at DESC, id DESC
        ) AS rn
        FROM outcomes
        WHERE result = 'unknown'
    )
    WHERE rn > 1
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_outcomes_one_open_draft
    ON outcomes (COALESCE(workspace_id, '00000000-0000-0000-0000-000000000000'), entity_type, entity_id)
    WHERE result = 'unknown';
