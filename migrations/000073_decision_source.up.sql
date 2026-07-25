-- 000073_decision_source.up.sql
--
-- Adds server-owned decision provenance to the decisions table: source is
-- 'manual' (written via a human-intent code path — an operator-triggered tool
-- call or HTTP request — which is NOT proof that a human confirmed it; see
-- internal/decision.Source's doc comment) or 'auto' (the system inferred it —
-- tool-call classifier, transcript classifier, or a TypeDecision proposal
-- materialiser). SA-2026-07-21-cold-read-resolved.
--
-- Design notes:
--   * NOT NULL DEFAULT 'manual' with a CHECK enum — the DEFAULT exists only
--     for migration/direct-SQL compatibility; every application insert binds
--     Source explicitly (internal/decision.LogParams.Source is a required
--     field validated by PG Store.Log and SQLite Log/LogTx before write,
--     backend-security-design.md §5.2-style exhaustive validation instead of
--     relying on the raw CHECK-violation error for UX).
--   * No FOREIGN KEY — not applicable here, plain enum column
--     (CLAUDE.md red-line #9 is about cross-table references).
--   * No new index: decisions.source is filtered alongside the existing
--     workspace_id/project_id/repo_name predicates at personal-OS scale
--     (backend-security-design.md §1.2 — indexes are advisory, add when a
--     query path is actually slow, not speculatively).
--   * Retention: follows the row's table TTL (decisions has none currently;
--     no separate TTL added for this column).
--   * ADD COLUMN (not IF NOT EXISTS): a re-run of this migration MUST fail
--     loudly (duplicate column error) instead of silently no-op'ing the
--     ALTER and falling through to the 3 backfill UPDATEs below against
--     already-migrated data (security review round 2, M-1).
--
-- Backfill: rows written by the pre-P3.0a auto-decision producers are
-- retagged 'auto' using the EXACT, case-sensitive, non-trimmed context/
-- rationale text those producers wrote historically. Anything that doesn't
-- match byte-for-byte (including NULL, case variants, or extra whitespace)
-- stays 'manual' — false negatives are acceptable, false positives are not
-- (provenance integrity). Down drops the column (provenance-lossy); re-up
-- recomputes ONLY these 3 predicates, see the down migration's comment.
--
-- created_at cutoff (security review round 2, M-1): context/rationale are
-- caller-supplied on every manual write path (internal/mcp/tools_decision.go
-- stringArg / HTTP req.Context) — an adversarial caller can call
-- log_decision with a context/rationale that byte-for-byte matches one of
-- the 3 predicates below, and without a time bound a REPLAY of this
-- migration (new environment, or a future down+up cycle) would silently
-- reclassify that caller-crafted 'manual' row as 'auto', dropping it out of
-- list_decisions' default (include_auto=false) view. All 3 UPDATEs are
-- therefore bounded to created_at before this migration's first production
-- apply time (2026-07-25 06:49 UTC, rounded up to the next whole hour:
-- 07:00:00Z) — only rows that already existed before P3.0a's provenance
-- column shipped are eligible for reclassification. Production's
-- schema_migrations is already at 73, so RunMigrations will not replay this
-- file there; the cutoff protects new environments and any future down+up
-- cycle, not prod's existing data (auto=505 / manual=333 at time of
-- writing, unaffected by this change).

ALTER TABLE decisions
    ADD COLUMN source TEXT NOT NULL DEFAULT 'manual'
        CHECK (source IN ('manual', 'auto'));

-- Predicate 1: internal/mcp/middleware_classify.go logMCPDecision. context
-- is "auto-extracted from MCP tool call: <toolName>" (varies per call), so
-- only the constant rationale text is matched.
UPDATE decisions
   SET source = 'auto'
 WHERE source = 'manual'
   AND rationale = 'implicitly decided via tool invocation'
   AND created_at < '2026-07-25T07:00:00Z'::timestamptz;

-- Predicate 2: internal/handler/autolog_handler.go logImplicitDecision.
UPDATE decisions
   SET source = 'auto'
 WHERE source = 'manual'
   AND context = 'auto-extracted from session transcript'
   AND rationale = 'implicitly decided during session'
   AND created_at < '2026-07-25T07:00:00Z'::timestamptz;

-- Predicate 3: internal/mcp/tools_proposal.go decodeDecisionParams and
-- internal/handler/proposal_handler.go decodeProposalDecisionPayload (the
-- TypeDecision proposal materialisers — MCP 3 backends + HTTP single/batch).
-- context is "auto-proposed by trigger_tool=<tool> session=<id>", where
-- trigger_tool/session_id are LLM-controlled tokens that may contain % or _.
-- strpos(...) = 1 is PG's non-LIKE prefix-match idiom (parity with the
-- SQLite twin's instr(...) = 1) so a wildcard-shaped trigger_tool/session_id
-- value can't cause a false LIKE match.
UPDATE decisions
   SET source = 'auto'
 WHERE source = 'manual'
   AND strpos(context, 'auto-proposed by trigger_tool=') = 1
   AND strpos(context, ' session=') > 0
   AND created_at < '2026-07-25T07:00:00Z'::timestamptz;
