-- 000084_repo_name_cleanup.up.sql (SQLite twin) — [F0925-33]
--
-- See migrations/000084_repo_name_cleanup.up.sql for full rationale. Same
-- eight tables as the PG twin — SQLite has no guard_events.repo_name column;
-- migrations/sqlite/000024_guard_events.up.sql is a documented no-op (guard
-- events only ever write to the Postgres backend).
--
-- Predicate: GLOB-based equivalent of ValidRepoPath (internal/validator/
-- repo_name.go:52). GLOB's '[A-Za-z]' character class is byte-range
-- ASCII-only under modernc.org/sqlite — verified empirically: 'café',
-- '日本語repo' and 'Ω-test' all match '[^A-Za-z0-9._/-]' (disallowed byte
-- present), while pure-ASCII 'abc123' and 'a_b-c.d' do not — so this does
-- NOT need the byte-enumeration workaround the Postgres twin's POSIX regex
-- uses for collation safety.
--   length(col) > 100          -> over the length limit
--   col GLOB '*[^A-Za-z0-9._/-]*' -> contains a disallowed byte (incl. non-ASCII)
--   col GLOB '[./-]*'          -> first byte is '.', '/' or '-'
--                                  (invalid segment start, or a leading slash)
--   col GLOB '*//*'            -> two slashes in a row (empty segment)
--   col GLOB '*/'              -> trailing slash (empty last segment)
--   col GLOB '*/[.-]*'         -> a non-first segment starts with '.' or '-'
--
-- Transaction: BEGIN TRANSACTION/COMMIT, same convention as
-- migrations/sqlite/000026_drop_fk_constraints.up.sql.
--
-- Ordering: work_sessions is deliberately LAST — same reasoning as the
-- Postgres twin (partial UNIQUE index idx_work_sessions_one_active,
-- migrations/sqlite/000021_work_sessions.up.sql:32).

BEGIN TRANSACTION;

UPDATE decisions
   SET repo_name = NULL
 WHERE repo_name IS NOT NULL AND repo_name <> ''
   AND (length(repo_name) > 100
        OR repo_name GLOB '*[^A-Za-z0-9._/-]*'
        OR repo_name GLOB '[./-]*'
        OR repo_name GLOB '*//*'
        OR repo_name GLOB '*/'
        OR repo_name GLOB '*/[.-]*');

UPDATE session_handoffs
   SET repo_name = NULL
 WHERE repo_name IS NOT NULL AND repo_name <> ''
   AND (length(repo_name) > 100
        OR repo_name GLOB '*[^A-Za-z0-9._/-]*'
        OR repo_name GLOB '[./-]*'
        OR repo_name GLOB '*//*'
        OR repo_name GLOB '*/'
        OR repo_name GLOB '*/[.-]*');

UPDATE vision_items
   SET repo_name = NULL
 WHERE repo_name IS NOT NULL AND repo_name <> ''
   AND (length(repo_name) > 100
        OR repo_name GLOB '*[^A-Za-z0-9._/-]*'
        OR repo_name GLOB '[./-]*'
        OR repo_name GLOB '*//*'
        OR repo_name GLOB '*/'
        OR repo_name GLOB '*/[.-]*');

UPDATE procedural_memories
   SET repo_name = ''
 WHERE repo_name IS NOT NULL AND repo_name <> ''
   AND (length(repo_name) > 100
        OR repo_name GLOB '*[^A-Za-z0-9._/-]*'
        OR repo_name GLOB '[./-]*'
        OR repo_name GLOB '*//*'
        OR repo_name GLOB '*/'
        OR repo_name GLOB '*/[.-]*');

UPDATE discipline_events
   SET repo_name = NULL
 WHERE repo_name IS NOT NULL AND repo_name <> ''
   AND (length(repo_name) > 100
        OR repo_name GLOB '*[^A-Za-z0-9._/-]*'
        OR repo_name GLOB '[./-]*'
        OR repo_name GLOB '*//*'
        OR repo_name GLOB '*/'
        OR repo_name GLOB '*/[.-]*');

UPDATE completion_candidates
   SET repo_name = NULL
 WHERE repo_name IS NOT NULL AND repo_name <> ''
   AND (length(repo_name) > 100
        OR repo_name GLOB '*[^A-Za-z0-9._/-]*'
        OR repo_name GLOB '[./-]*'
        OR repo_name GLOB '*//*'
        OR repo_name GLOB '*/'
        OR repo_name GLOB '*/[.-]*');

UPDATE projects
   SET repo_name = NULL
 WHERE repo_name IS NOT NULL AND repo_name <> ''
   AND (length(repo_name) > 100
        OR repo_name GLOB '*[^A-Za-z0-9._/-]*'
        OR repo_name GLOB '[./-]*'
        OR repo_name GLOB '*//*'
        OR repo_name GLOB '*/'
        OR repo_name GLOB '*/[.-]*');

-- work_sessions LAST — see the ordering note above.
UPDATE work_sessions
   SET repo_name = ''
 WHERE repo_name IS NOT NULL AND repo_name <> ''
   AND (length(repo_name) > 100
        OR repo_name GLOB '*[^A-Za-z0-9._/-]*'
        OR repo_name GLOB '[./-]*'
        OR repo_name GLOB '*//*'
        OR repo_name GLOB '*/'
        OR repo_name GLOB '*/[.-]*');

COMMIT;
