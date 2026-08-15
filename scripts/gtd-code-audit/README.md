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
gap is that a human has to remember to run it. `_project/CLAUDE.md`'s manual
scripts table is where that reminder should live (this repo's `scripts/`
directory is not itself wired to any hook) — flagged to Lead in the
dispatch completion report rather than self-edited, since `.claude/**` is
out of scope for this ticket.

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

Every run (unless `--no-write`) writes a dated report to `reports/` — same
convention as `scripts/ai-review/scan-review.py` and
`scripts/pitfalls-gc/pitfalls-gc.py`. There is **no `--apply` flag and no
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

`test_audit.py`'s `TestGitOpsRejectsUnsafeGrepInput` and the adversarial
cases in `TestExtractAnchors`/`TestSafePath` exercise this directly —
overlong literals, shell metacharacters, path traversal, null bytes.

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

- `positive_verified` (10 tickets: all 4 PR#151 findings + 6 of PR#157's
  original 11-ticket wave) — manually confirmed already fixed in code
  *before* writing the corresponding rule, by reading the actual current
  file content (not by trusting ticket text or GTD status — several of
  these were still `pending` in GTD despite the code fix already existing).
  `test_audit.py::test_verified_positive_fixtures_flagged_suspect` asserts
  every one of these gets `SUSPECT_ALREADY_FIXED`.
- `negative_verified` (1 ticket: `7610088f`, `GET /api/projects` still
  active-only) — manually confirmed still broken by reading the handler.
  `test_verified_negative_fixtures_not_flagged_suspect` asserts this is
  never flagged `SUSPECT_ALREADY_FIXED`.
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
