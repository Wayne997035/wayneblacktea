-- 000084_repo_name_cleanup.up.sql
--
-- [F0925-33] Clears repo_name values that violate the write-path rule
-- (internal/validator/repo_name.go ValidRepoPath / IsValidRepoName, :52 and
-- :105) from every table where repo_name predates that rule's enforcement.
-- New writes have been rejected since REPONAME-01 shipped; this is a
-- one-time cleanup of values written before that landed.
--
-- Scope — the nine tables with a repo_name column: decisions (000003:7),
-- session_handoffs (000004:7), work_sessions (000021:13, NOT NULL),
-- guard_events (000024:13, Postgres only — the SQLite twin,
-- migrations/sqlite/000024_guard_events.up.sql, is a documented no-op),
-- vision_items (000029:18), procedural_memories (000032:4, NOT NULL DEFAULT
-- ''), discipline_events (000035:19), completion_candidates (000042:5),
-- projects (000037:15). Key columns (repos.name, project_arch.slug,
-- project_status_snapshots.slug) are deliberately left untouched (decision
-- 3be90339) — this migration only clears repo_name.
--
-- No backup, no restore: original values are not recoverable (decision
-- 3be90339). down.sql is a documented no-op — see that file.
--
-- Predicate: the rejected side of ValidRepoPath — over 100 bytes, or not
-- "one or more '/'-separated segments, each starting with a letter/digit/
-- '_' and continuing with letters/digits/'.'/'_'/'-'". The character class
-- is spelled out byte-by-byte (ABCDEFG...) instead of as an `A-Z` range:
-- POSIX regex bracket-expression ranges are collation-dependent in
-- Postgres, an explicit enumeration is not.
--
-- Transaction: BEGIN/COMMIT wraps every UPDATE below in one transaction, so
-- a failure partway through (see the work_sessions note) rolls back every
-- UPDATE already applied in this file, not just the failing statement.
--
-- Ordering: work_sessions is deliberately LAST. work_sessions carries a
-- partial UNIQUE index, idx_work_sessions_one_active ON (workspace_id,
-- repo_name) WHERE status = 'in_progress' (000021:42). Clearing repo_name
-- to '' on two or more in_progress rows in the same workspace collides with
-- that index and the UPDATE errors. That is accepted behaviour, not a bug:
-- the whole transaction rolls back and deployment fails loudly (decision
-- 3be90339) rather than silently de-duplicating or leaving one row unclean.
-- Placing it last lets the transactional integration test prove every
-- earlier UPDATE in this file was rolled back too.

BEGIN;

UPDATE decisions
   SET repo_name = NULL
 WHERE repo_name IS NOT NULL AND repo_name <> ''
   AND (length(repo_name) > 100
        OR repo_name !~ '^[ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789_][ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789._-]*(/[ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789_][ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789._-]*)*$');

UPDATE session_handoffs
   SET repo_name = NULL
 WHERE repo_name IS NOT NULL AND repo_name <> ''
   AND (length(repo_name) > 100
        OR repo_name !~ '^[ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789_][ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789._-]*(/[ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789_][ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789._-]*)*$');

UPDATE guard_events
   SET repo_name = NULL
 WHERE repo_name IS NOT NULL AND repo_name <> ''
   AND (length(repo_name) > 100
        OR repo_name !~ '^[ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789_][ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789._-]*(/[ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789_][ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789._-]*)*$');

UPDATE vision_items
   SET repo_name = NULL
 WHERE repo_name IS NOT NULL AND repo_name <> ''
   AND (length(repo_name) > 100
        OR repo_name !~ '^[ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789_][ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789._-]*(/[ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789_][ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789._-]*)*$');

UPDATE procedural_memories
   SET repo_name = ''
 WHERE repo_name IS NOT NULL AND repo_name <> ''
   AND (length(repo_name) > 100
        OR repo_name !~ '^[ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789_][ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789._-]*(/[ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789_][ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789._-]*)*$');

UPDATE discipline_events
   SET repo_name = NULL
 WHERE repo_name IS NOT NULL AND repo_name <> ''
   AND (length(repo_name) > 100
        OR repo_name !~ '^[ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789_][ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789._-]*(/[ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789_][ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789._-]*)*$');

UPDATE completion_candidates
   SET repo_name = NULL
 WHERE repo_name IS NOT NULL AND repo_name <> ''
   AND (length(repo_name) > 100
        OR repo_name !~ '^[ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789_][ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789._-]*(/[ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789_][ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789._-]*)*$');

UPDATE projects
   SET repo_name = NULL
 WHERE repo_name IS NOT NULL AND repo_name <> ''
   AND (length(repo_name) > 100
        OR repo_name !~ '^[ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789_][ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789._-]*(/[ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789_][ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789._-]*)*$');

-- work_sessions LAST — see the ordering note above.
UPDATE work_sessions
   SET repo_name = ''
 WHERE repo_name IS NOT NULL AND repo_name <> ''
   AND (length(repo_name) > 100
        OR repo_name !~ '^[ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789_][ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789._-]*(/[ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789_][ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789._-]*)*$');

COMMIT;
