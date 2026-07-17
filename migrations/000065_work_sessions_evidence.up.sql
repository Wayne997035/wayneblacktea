-- Migration 000065: work_sessions evidence + verification columns
--
-- wbt-2.0 Phase 2 "Action Lifecycle And Evidence Chain" (P2.1).
--
-- Adds evidence-chain fields to work_sessions so a session can record:
--   * which assembled context pack (Phase 1 assemble_context) it started from
--   * whether/how its work was verified (command + status + output excerpt)
--   * its terminal result and a link to the outcomes row recording it
--   * the git branch the session worked on
--
-- NO FOREIGN KEY per CLAUDE.md red-line #9. context_pack_id and outcome_id
-- are nullable UUID columns with referential integrity enforced app-side
-- (worksession.Store.SetOutcomeLink / GetByID tolerate stale/missing links).
ALTER TABLE work_sessions ADD COLUMN IF NOT EXISTS context_pack_id UUID NULL;

ALTER TABLE work_sessions ADD COLUMN IF NOT EXISTS verification_status TEXT NULL
    CHECK (verification_status IN ('not_run','passed','failed','unknown'));

ALTER TABLE work_sessions ADD COLUMN IF NOT EXISTS verification_command TEXT NULL;

ALTER TABLE work_sessions ADD COLUMN IF NOT EXISTS verification_output_excerpt TEXT NULL;

ALTER TABLE work_sessions ADD COLUMN IF NOT EXISTS outcome_id UUID NULL;

ALTER TABLE work_sessions ADD COLUMN IF NOT EXISTS final_result TEXT NULL
    CHECK (final_result IN ('success','failure','partial','unknown','regressed'));

ALTER TABLE work_sessions ADD COLUMN IF NOT EXISTS branch_name TEXT NULL;
