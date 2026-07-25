-- 000073_decision_source.up.sql (SQLite twin)
--
-- See migrations/000073_decision_source.up.sql for full design rationale.
-- SQLite 3.31+ allows ADD COLUMN ... CHECK as long as the constraint holds
-- against the DEFAULT value applied to existing rows (verified empirically:
-- 'manual' satisfies CHECK (source IN ('manual','auto')) for every
-- pre-existing row; confirmed no earlier SQLite migration in this repo
-- relies on an unsupported ADD COLUMN + CHECK combination either).
--
-- instr(haystack, needle) = 1 is SQLite's non-LIKE prefix-match idiom —
-- parity with the Postgres twin's strpos(...) = 1 — needed because
-- trigger_tool/session_id values are LLM-controlled tokens that may contain
-- % or _ (LIKE wildcard injection risk if a LIKE '...%' pattern were used
-- instead).
--
-- created_at cutoff (security review round 2, M-1): see the Postgres twin's
-- comment for the full rationale — context/rationale are caller-controlled,
-- so a migration replay without a time bound could reclassify an
-- adversarially crafted 'manual' row as 'auto'. created_at is TEXT (RFC3339
-- with millisecond precision — migrations/sqlite/000012_sqlite_baseline.up.sql's
-- `strftime('%Y-%m-%dT%H:%M:%fZ','now')` DEFAULT and
-- internal/storage/sqlite/decision.go's sqliteMillisLayout both produce this
-- fixed-width zero-padded shape), so a plain TEXT `<` comparison against the
-- same cutoff timestamp used in the Postgres twin sorts chronologically.
-- Note: unlike the Postgres twin, this ALTER already had no IF NOT EXISTS
-- form (SQLite doesn't support it) — a re-run already fails loudly.

ALTER TABLE decisions ADD COLUMN source TEXT NOT NULL DEFAULT 'manual'
    CHECK (source IN ('manual','auto'));

-- Predicate 1: internal/mcp/middleware_classify.go logMCPDecision.
UPDATE decisions
   SET source = 'auto'
 WHERE source = 'manual'
   AND rationale = 'implicitly decided via tool invocation'
   AND created_at < '2026-07-25T07:00:00.000Z';

-- Predicate 2: internal/handler/autolog_handler.go logImplicitDecision.
UPDATE decisions
   SET source = 'auto'
 WHERE source = 'manual'
   AND context = 'auto-extracted from session transcript'
   AND rationale = 'implicitly decided during session'
   AND created_at < '2026-07-25T07:00:00.000Z';

-- Predicate 3: TypeDecision proposal materialisers (MCP + HTTP).
UPDATE decisions
   SET source = 'auto'
 WHERE source = 'manual'
   AND instr(context, 'auto-proposed by trigger_tool=') = 1
   AND instr(context, ' session=') > 0
   AND created_at < '2026-07-25T07:00:00.000Z';
