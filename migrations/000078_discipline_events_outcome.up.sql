-- 000078_discipline_events_outcome.up.sql
-- [F184-04] Adds four outcome/observability columns to discipline_events:
-- ok, error_class, response_bytes, duration_ms. Lets discipline_events
-- answer "which tool, how often does it fail, how large is each response" —
-- previously, disciplineMiddleware early-returned on any failed call, so
-- discipline_events only ever recorded successful mutations.
--
-- Design notes:
--   * ok BOOLEAN NOT NULL DEFAULT TRUE — every row written before this
--     migration was, by construction of the pre-migration early-return, a
--     successful call, so backfilling TRUE is factually accurate (unlike
--     e.g. 000076's confirmed_by_human FALSE default, which encodes "not
--     proven" rather than a known fact — see that migration's comment).
--   * error_class TEXT — nullable, no default. NULL for ok=TRUE rows.
--     No CHECK constraint: validated in Go via the new ErrorClass type at
--     the insert boundary (avoids an ALTER TABLE ADD COLUMN ... CHECK
--     syntax-support asymmetry between PG/SQLite; this migration set only
--     ever uses CHECK in CREATE TABLE, e.g. discipline_events_m8's
--     event_type/severity, not replicated here).
--   * response_bytes INTEGER — nullable, no default. Pre-migration rows
--     never measured response size.
--   * duration_ms INTEGER — nullable, no default. Pre-migration rows never
--     measured wall-clock duration.
--   * No foreign-key constraint (CLAUDE.md red-line #9 / backend-security-
--     design.md §1.1) — none of these columns reference another table.
--   * No new index (backend-security-design.md §1.2, indexes are
--     advisory): no query/filter path in this repo reads error_class /
--     response_bytes / duration_ms yet. RecentMutating and
--     RecentDecisionTimes gain an `ok` equality filter appended to an
--     already-filtered WHERE clause (is_mutating / session_id /
--     observed_at / workspace_id), not a new standalone predicate that
--     would benefit from its own index.
--   * No backfill UPDATE beyond ok's DEFAULT TRUE: error_class /
--     response_bytes / duration_ms have no honest non-NULL value for
--     pre-migration rows (a synthetic placeholder would be
--     indistinguishable from a real measurement on read).
--   * ADD COLUMN (not IF NOT EXISTS): mirrors 000073/000076 — a re-run of
--     this migration MUST fail loudly (duplicate column error) instead of
--     silently no-op'ing.

ALTER TABLE discipline_events ADD COLUMN ok BOOLEAN NOT NULL DEFAULT TRUE;
ALTER TABLE discipline_events ADD COLUMN error_class TEXT;
ALTER TABLE discipline_events ADD COLUMN response_bytes INTEGER;
ALTER TABLE discipline_events ADD COLUMN duration_ms INTEGER;
