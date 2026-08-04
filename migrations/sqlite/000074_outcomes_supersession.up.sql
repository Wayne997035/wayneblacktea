-- 000074_outcomes_supersession.up.sql (SQLite twin)
-- See migrations/000074_outcomes_supersession.up.sql for the Postgres source
-- of truth and full design rationale (GTD decision 80c1e8ae, arch-r2 A13).
--
-- Dialect differences: UUID -> TEXT. Partial + expression unique indexes are
-- supported by SQLite since 3.8/3.9 (modernc.org/sqlite v1.50.0 bundles a
-- far newer SQLite), including as an ON CONFLICT target for UPSERT
-- (SQLite 3.24+), which internal/storage/sqlite/outcome.go's SeedDraft
-- relies on.
--
-- No FOREIGN KEY per CLAUDE.md red-line #9.
ALTER TABLE outcomes ADD COLUMN supersedes_id TEXT;

CREATE INDEX IF NOT EXISTS idx_outcomes_supersedes_id
    ON outcomes(supersedes_id) WHERE supersedes_id IS NOT NULL;

CREATE UNIQUE INDEX IF NOT EXISTS idx_outcomes_one_open_draft
    ON outcomes (COALESCE(workspace_id, '00000000-0000-0000-0000-000000000000'), entity_type, entity_id)
    WHERE result = 'unknown';
