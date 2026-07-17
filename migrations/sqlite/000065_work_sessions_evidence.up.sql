-- 000065_work_sessions_evidence.up.sql (SQLite twin)
--
-- Adds evidence-chain + verification columns to work_sessions for SQLite
-- databases. Mirrors migrations/000065_work_sessions_evidence.up.sql (Postgres).
--
-- SQLite 3.35+ supports ALTER TABLE ... ADD COLUMN. All 7 columns are
-- nullable with no DEFAULT (back-compatible; existing rows keep NULL).
--
-- Fresh databases: schema.sql already declares these columns via
-- CREATE TABLE IF NOT EXISTS, so this file only matters for pre-existing
-- SQLite databases. The idempotent applyColumnUpgrades() path in db.go
-- Open() issues the same ALTER TABLE statements on startup, ignoring
-- "duplicate column name" errors — this migration file records the intent
-- for reviewers following the §6.3 dual-backend parity rule.
--
-- No FOREIGN KEY per CLAUDE.md red-line #9; context_pack_id/outcome_id
-- integrity enforced app-side.
ALTER TABLE work_sessions ADD COLUMN context_pack_id TEXT;

ALTER TABLE work_sessions ADD COLUMN verification_status TEXT
    CHECK (verification_status IN ('not_run','passed','failed','unknown'));

ALTER TABLE work_sessions ADD COLUMN verification_command TEXT;

ALTER TABLE work_sessions ADD COLUMN verification_output_excerpt TEXT;

ALTER TABLE work_sessions ADD COLUMN outcome_id TEXT;

ALTER TABLE work_sessions ADD COLUMN final_result TEXT
    CHECK (final_result IN ('success','failure','partial','unknown','regressed'));

ALTER TABLE work_sessions ADD COLUMN branch_name TEXT;
