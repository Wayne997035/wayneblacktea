# gtd-code-audit

Triage GTD tickets against what the code currently looks like, and flag ones
that are suspiciously likely to already be fixed. **It never closes a
ticket** — it only produces a graded list of suspects with re-runnable
verification commands for a human (or a reviewing agent) to check.

## Why this exists

Happened twice in one day (2026-08-15):

- PR #157's review findings opened 11 GTD tickets. All 11 fixes landed in
  the single merged commit. None of the 11 got closed.
- PR #151's review findings opened 4 tickets. All 4 fixes landed in one
  merge commit. None got closed — and nobody checked before dispatching, so
  3 engineers opened 3 worktrees to "fix" code that was already fixed,
  producing zero lines of real work.

This is the check that should have run before that second dispatch.

## When to run it

**Before dispatching an engineer against any `pending`/`fix-pr` GTD ticket
that references specific code** (has a `file:line`, a function/constant
name, or a PR review round-label in its title/body). Concretely: right
after `list_tasks`/`get_today_context` surfaces candidate work and before
`update_task(status="in_progress")` + dispatch.

**This is a manual step — nothing calls it automatically.** A ticket sitting
in `pending` status is not, by itself, a signal to re-run this; the residual
gap is that a human has to remember to run it. This repo's `scripts/`
directory is not wired to any hook, so the reminder has to live wherever
your workspace keeps its list of manual steps.

## Usage

```bash
# Scan all pending tickets against the repo containing this script
python3 audit.py

# Check specific tickets by UUID (comma-separated)
python3 audit.py --ids <uuid>,<uuid>

# Offline / no DB — read tickets from a JSON file (see testdata/ for shape)
python3 audit.py --tasks-json testdata/known_tickets.json

# Machine-readable output
python3 audit.py --json

# Check a different checkout
python3 audit.py --repo /path/to/other/checkout
```

Every run (unless `--no-write`) writes a dated report to `reports/`. There
is **no `--apply` flag and no
write path to the `tasks` table anywhere in this package** — `lib/db.py`
only ever issues `SELECT`.

## What it does

For each ticket, `lib/extract.py` pulls out machine-checkable anchors
(pure text-in/dict-out, no I/O): `file:line` references, Go-shaped
identifiers (camelCase / PascalCase / snake_case / kebab-case), PR review
round-labels (`M-4`, `m-R2`, `C-1`), commit SHAs cued by the word "commit",
and comma-grouped measurement numbers (`80,687`).

`lib/rules.py` runs those anchors through a **priority-ordered decision
tree** — never a blended/weighted score. Each ticket gets exactly one
verdict: the first rule that fires, every rule carrying an exact
`git grep`/`git log` command to re-check its own work.

| Verdict | Meaning |
|---|---|
| `SUSPECT_ALREADY_FIXED` | Corroborating evidence found — go verify before dispatching |
| `CITED_BUT_MARKED_UNFIXED` | A citation matched, but the surrounding code text itself says it's not fixed yet |
| `ANCHOR_LINE_OUT_OF_RANGE` | File exists, referenced line no longer does (refactored) |
| `SYMBOL_NOT_FOUND_POSSIBLY_RENAMED` | Named symbol doesn't exist anywhere by that name |
| `STILL_OPEN_LIKELY` | Anchor file untouched since the ticket was filed — confident negative |
| `UNKNOWN_NEEDS_HUMAN` | Anchors exist but no rule fired either way |
| `NO_ANCHOR_NEEDS_HUMAN` | Nothing machine-checkable in the ticket text |

## Threat model

GTD ticket text is writable by any agent, including a prompt-injected one —
treat it as adversarial. See `lib/gitutil.py`'s module docstring for the
full argument; the short version:

- Every anchor string reaches the filesystem/subprocess through exactly one
  module (`lib/gitutil.py`). `subprocess` is always called with an **argv
  list, never `shell=True`** — a payload like `` `$(rm -rf ~)` `` in a
  ticket description just fails to match anything in `git grep -F`, it
  never reaches a shell.
- Every path is resolved through `safe_path()`: null bytes / CR / LF /
  leading `~` / absolute paths rejected outright; the rest is
  symlink-resolved and containment-checked against `repo_root` before any
  filesystem access.
- `lib/db.py`'s SQL is built from `status`/`ids` CLI flags only — each
  validated against an explicit allowlist (status enum) or a strict UUID
  regex before touching the query string. Ticket body text never reaches
  SQL.
- `grep_fixed()` excludes this tool's own directory
  (`lib/gitutil.py::SELF_EXCLUDE_PATHSPEC`) from every repo-wide search. A
  ticket's own text (branch names, quoted code) ends up stored verbatim in
  `testdata/known_tickets.json` and in adversarial test payloads in
  `test_audit.py`, both git-tracked — without this exclusion, a ticket can
  corroborate itself just by existing in this tool's own fixtures (found
  via fixture regression: ticket 6f306618's branch-name mention only
  self-matched inside `testdata/known_tickets.json`, producing a false
  `CITED_BUT_MARKED_UNFIXED`).
- **Anchor counts are capped** (`lib/extract.py::MAX_ANCHORS_PER_CATEGORY`,
  currently 50 per category) and `grep_fixed`/`last_commit_iso` are
  memoized per `GitOps` instance (`lib/gitutil.py`). Ticket `description` is
  an unbounded `TEXT` column with no length cap at the `add_task` MCP tool
  either, so a ticket padded with thousands of unique symbol-shaped tokens
  is a real, reachable DoS against this script, not a theoretical one —
  measured directly: an uncapped run against 300 unique non-matching
  symbols took 37.1s (~0.124s per `git grep` subprocess call); extrapolated
  to a ~40,000-token payload that's over an hour for a single ticket. After
  the cap+memoization fix, the same shape at any input size (300 tokens or
  5,000) classifies in ~5.5s — bounded by the cap, not by attacker input
  size. A truncated ticket's `Evidence.reasons` always carries a `⚠` note
  naming which categories were capped and by how many (never a silent
  drop) — see `lib/rules.py::_truncation_notice`.

`test_audit.py`'s `TestGitOpsRejectsUnsafeGrepInput`, `TestGitOpsSelfExclusion`,
`TestAnchorCapAndMemoization`, and the adversarial cases in
`TestExtractAnchors`/`TestSafePath` exercise this directly — overlong
literals, shell metacharacters, path traversal, null bytes, self-citation,
and anchor flooding.

### Subprocess call-count bounds (per ticket, worst case, first touch — no cache warm)

Every subprocess this package spawns funnels through exactly one place,
`GitOps._run` (`lib/gitutil.py`) — `db.py`'s single `psql` call (once per
script invocation, not per ticket) is the only exception. Bound derivation
below assumes the 50-per-category cap and holds regardless of how large the
adversarial ticket text is (extraction truncates before any of this runs):

| Call site (via `rules.classify`) | Method | Per-ticket bound | Notes |
|---|---|---|---|
| `_label_citation_hit` — PR-number variants | `grep_fixed` | ≤100 (50 PR numbers × 2 literal formats) | not memoized across formats (`PR #N` / `PR#N` are distinct literals) |
| `_label_citation_hit` — labels | `grep_fixed` | ≤50 (deduped) | measured: naively O(pr×label) would be ≤2,500 without memoization; with it, 150 calls total observed for a 50×50 adversarial ticket (16.9s) vs a theoretical ~2,600 calls (~5 min) without |
| measurement citation loop | `grep_fixed` | ≤50 | stops at first hit |
| `_symbol_citation_hit` | `grep_fixed` | ≤50 | stops at first hit; measured 5.5s worst case (50 misses) |
| recency checks (`_symbol_citation_hit`'s touched-after loop + the `STILL_OPEN_LIKELY` fallback loop) | `last_commit_iso` | ≤50 (deduped across both call sites) | same anchor-file set both times |
| `resolve_basename` (via `_resolve_file`) | `ls-files` | 1, ever | cached at `GitOps` construction — not per-ticket, not per-anchor |
| `file_exists` / `line_count` / `read_context` | — | 0 | direct filesystem I/O, no subprocess |

Sum of the bounded rows ≈ 300 calls (~0.12s each) ≈ 36s worst case for a
never-before-seen ticket; a batch run (`audit.py`'s normal usage) reuses one
`GitOps` instance across every ticket, so subsequent tickets sharing labels/
PR numbers/anchor files with earlier ones in the same run pay far less —
measured: 5 identical adversarial tickets back-to-back after the first,
0.004s total.

## Known limitations (found via fixture testing, not theoretical)

- **Symbol citation can't fully distinguish "generic/vendor term" from
  "real project identifier"** — e.g. `golangci-lint` or `MaxConns` can
  clear the specificity bar without actually corroborating the ticket's
  specific finding. A file-spread cutoff (reject symbols appearing in more
  than N files) was tried and reverted: it also rejected legitimately
  widespread *project* identifiers (`file_map`, `checkCommandField`) that
  are exactly the strong evidence this rule exists to find — see
  `lib/rules.py::_symbol_citation_hit` docstring. The mitigation is
  procedural: every verdict carries the matched symbol and a re-runnable
  grep command, so a human sees at a glance when the citation is weak.
- **This is triage, not judgement.** A `SUSPECT_ALREADY_FIXED` verdict means
  "go look here", not "this is fixed". `git_task_id`'s `complete_task` call
  is still a human/agent decision after reading the verify command's
  output — the script has no opinion on correctness, only on whether
  something changed near where the ticket points.

## Fixture provenance (`testdata/known_tickets.json`)

16 real tickets from wayneblacktea's own GTD, captured 2026-08-15. Each is
tagged `fixture_category`:

- `positive_verified` (11 tickets: all 4 PR#151 findings + 6 of PR#157's
  original 11-ticket wave + `7610088f`, moved here — see below) — manually
  confirmed already fixed in code *before* writing the corresponding rule,
  by reading the actual current file content (not by trusting ticket text
  or GTD status — several of these were still `pending`/`in_progress` in
  GTD despite the code fix already existing).
  `test_audit.py::test_verified_positive_fixtures_flagged_suspect` asserts
  every one of these gets `SUSPECT_ALREADY_FIXED`.
- `negative_verified` (0 tickets, by design — see below). Historically held
  1 ticket (`7610088f`, `GET /api/projects` returning active-only) which was
  **moved to `positive_verified` during GTD `fix/gtd-audit-tests-and-gate`**:
  commit `582741d` (`fix: GET /api/projects now applies the status query
  param instead of ignoring it`) already implements the fix — and that
  commit predates this tool's own first commit (`9ac008a`), so the
  `negative_verified` classification was stale the moment this fixture was
  frozen, not something that rotted later. This surfaced a structural
  problem with anchoring a "must never false-positive" regression guard to
  a real GTD bug ticket: the entire point of filing a bug is that it
  eventually gets fixed, so a real ticket is the wrong anchor for a
  guarantee that's supposed to hold forever.
  `test_verified_negative_fixtures_not_flagged_suspect` is kept (now
  vacuously passing) so a future manually-verified still-open ticket can be
  dropped in without new test code, but the guard that actually matters now
  is `test_synthetic_permanent_tradeoff_never_flagged_suspect`: it anchors
  to `internal/storage/sqlite/knowledge.go`'s `SearchByCosine`, an
  *accepted* design trade-off (SQLite has no ANN index — documented in
  `wayneblacktea/CLAUDE.md`, not tracked as a bug), and computes
  `created_at` **at test-run time** from that file's own last-commit
  timestamp rather than trusting a frozen calendar date — the exact axis
  that made `7610088f` rot.
- `positive_claimed_by_dispatch` (5 tickets) — **the dispatch that
  commissioned this script said all 11 of PR#157's original wave "should be
  flagged already-fixed"; direct code inspection while building the rules
  found 2 of these are genuinely still-open design debt** (their own ticket
  text says "不擋 merge", i.e. explicitly accepted as a known limitation,
  not a closed bug) **and 3 are policy/documentation decisions with no
  single code anchor** (e.g. "decide whether to include `cmd/server` in
  lint scope" — there's no file:line whose presence/absence proves a
  decision was made). Forcing the regression test to assert
  `SUSPECT_ALREADY_FIXED` on these would have meant loosening the rules
  until they produced false positives — exactly the failure mode this tool
  exists to prevent. `test_dispatch_claimed_fixtures_report_honestly`
  prints (doesn't assert) what the script says about each, so this
  discrepancy stays visible in test output.

## Files

- `audit.py` — CLI entry point
- `lib/extract.py` — pure anchor extraction (title+description → dict)
- `lib/rules.py` — the decision tree
- `lib/gitutil.py` — the only module touching the filesystem/subprocess
- `lib/db.py` — read-only psql wrapper (SELECT only)
- `test_audit.py` — unit tests (pure, no repo) + regression suite (real
  `GitOps` against the real repo, frozen fixture set)
- `testdata/known_tickets.json` — the 16-ticket fixture set above
