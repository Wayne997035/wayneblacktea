"""rules — the verdict decision tree.

Design mandate (Lead, gtd-code-audit dispatch): this is triage, not
judgement. The tree below is a PRIORITY-ORDERED list of discrete,
independently-reportable rules — never a weighted/summed score. Each ticket
gets exactly one verdict: the first rule that fires. Every rule that fires
carries the exact grep/log command a human can re-run to check its work.

Verdicts, most to least confident:
  SUSPECT_ALREADY_FIXED        — corroborating evidence found; go verify
  ANCHOR_LINE_OUT_OF_RANGE     — file exists, referenced line no longer does
                                  (file shrank/was refactored) — degraded case ①
  SYMBOL_NOT_FOUND_POSSIBLY_RENAMED — named symbol doesn't exist anywhere in
                                  repo by that exact name — degraded case ②
  STILL_OPEN_LIKELY            — anchor file exists, unchanged since the
                                  ticket was filed — confident negative
  UNKNOWN_NEEDS_HUMAN          — anchors exist but none of the above fired
                                  (weak/ambiguous evidence either way)
  NO_ANCHOR_NEEDS_HUMAN        — ticket has nothing machine-checkable
  CITED_BUT_MARKED_UNFIXED     — a citation matched, but the surrounding
                                  code text itself says the thing is not
                                  fixed (see STILL_OPEN_MARKER_RE below)
"""
import re
from dataclasses import dataclass, field
from datetime import datetime

from . import extract as extract_mod
from .gitutil import GitOps

DEFAULT_WINDOW = 80
CITATION_CONTEXT_RADIUS = 3

# This team's own code-comment convention self-documents known-unfixed
# items with these phrases (found independently twice while building this
# tool: tools_arch.go "Known trade-off, NOT fixed by this change" and
# resources.go "...an unbounded cost..." describing exactly the limit a
# ticket complained about, with no cap actually added). A citation match
# whose surrounding lines contain one of these is describing the PROBLEM,
# not a fix for it — firing SUSPECT_ALREADY_FIXED there would be the
# costly false positive Lead's dispatch explicitly warned against.
STILL_OPEN_MARKER_RE = re.compile(
    r'not fixed by this change|NOT fixed|unbounded cost|known limitation'
    r'|known trade-off|accepted trade-off|不擋 ?merge|尚未修|待修|TODO\b',
    re.I,
)


@dataclass
class Evidence:
    verdict: str
    reasons: list = field(default_factory=list)
    verify_commands: list = field(default_factory=list)


def _parse_ts(s: str):
    if not s:
        return None
    try:
        return datetime.fromisoformat(s.replace("Z", "+00:00"))
    except ValueError:
        return None


def _resolve_file(git: GitOps, path: str) -> str | None:
    """Direct lookup first; fall back to unique-basename resolution for
    ticket text that dropped the directory prefix."""
    if git.file_exists(path):
        return path
    basename = path.rsplit("/", 1)[-1]
    resolved = git.resolve_basename(basename)
    return resolved if resolved and git.file_exists(resolved) else None


def _label_citation_hit(git: GitOps, pr_numbers, labels, window):
    """Rule: a review-round label (M-4, m-R2, ...) and its PR number both
    appear within `window` lines of each other in the SAME file. This is
    the team's own established citation convention (verified empirically:
    resources.go:490 'PR #157 security review C-1 / M-4') — presence of
    both together is much stronger evidence than either alone, because
    round-labels are otherwise dense/repeated within a single review-heavy
    file (a bare PR-number match alone produced a false positive during
    fixture testing)."""
    for pr in pr_numbers:
        pr_hits = git.grep_fixed(f"PR #{pr}") + git.grep_fixed(f"PR#{pr}")
        if not pr_hits:
            continue
        for label in labels:
            label_hits = git.grep_fixed(label)
            for lf, ll in label_hits:
                for pf, pl in pr_hits:
                    if lf == pf and abs(pl - ll) <= window:
                        return (lf, ll, label, pr)
    return None


def _still_open_marked(git: GitOps, file: str, line: int) -> bool:
    ctx = git.read_context(file, line, radius=CITATION_CONTEXT_RADIUS)
    return bool(STILL_OPEN_MARKER_RE.search(ctx))


def _symbol_citation_hit(git: GitOps, symbols):
    """Rule: a non-generic identifier extracted from the ticket (>=5 chars,
    Go-shaped) exists anywhere in the repo. Returns the first hit.

    KNOWN LIMITATION (documented, not silently papered over): a symbol
    that's a widely-used vendor/tool name (e.g. "golangci-lint") or an
    overly-generic-sounding constant ("MaxConns") can pass this check
    without actually corroborating the ticket's specific finding. An
    file-spread cutoff was tried and reverted — it also rejected
    legitimately widespread PROJECT identifiers ("file_map",
    "checkCommandField") that are exactly the strong evidence this rule
    exists to find, which is a worse trade (see README.md "Known
    limitations"). The mitigation is procedural, not mechanical: every
    SUSPECT_ALREADY_FIXED verdict carries the exact matched symbol and a
    re-runnable grep command specifically so a human can see at a glance
    when the cited symbol is this kind of weak, generic match.

    Longer identifiers are tried first (not alphabetical) — a longer
    Go-shaped token is typically more specific to the one finding than a
    short one ("checkCommandField" vs "Client"), so trying long-to-short
    surfaces the more specific match when more than one symbol has a hit."""
    for sym in sorted(symbols, key=len, reverse=True):
        hits = git.grep_fixed(sym)
        if hits:
            return (sym, hits[0])
    return None


def _truncation_notice(anchors: dict) -> str | None:
    """Anchor extraction caps each category at
    extract.MAX_ANCHORS_PER_CATEGORY (DoS guard — see that constant's
    docstring). Per this tool's own "no silent caps" policy, a ticket that
    hit the cap on any category MUST carry a visible note saying so, not
    silently classify against a truncated subset with no trace of the
    truncation. Prepended to Evidence.reasons by classify() below,
    regardless of which rule ultimately fires."""
    if not anchors.get("truncated"):
        return None
    parts = ", ".join(f"{cat} 截掉 {n} 個" for cat, n in sorted(anchors["truncated"].items()))
    return f"⚠ 錨點數量超過上限(每類最多 {extract_mod.MAX_ANCHORS_PER_CATEGORY} 個),{parts} —— 判定只根據保留下來的子集"


def classify(title: str, description: str, created_at: str, git: GitOps,
             window: int = DEFAULT_WINDOW) -> Evidence:
    anchors = extract_mod.extract_anchors(title, description)
    notice = _truncation_notice(anchors)
    ev = _classify_inner(created_at, git, anchors, window)
    if notice:
        ev.reasons = [notice] + ev.reasons
    return ev


def _classify_inner(created_at: str, git: GitOps, anchors: dict,
                     window: int = DEFAULT_WINDOW) -> Evidence:
    if not extract_mod.has_any_anchor(anchors):
        return Evidence("NO_ANCHOR_NEEDS_HUMAN", [
            "沒有可機器檢查的錨點(無 file:line / 符號 / commit SHA / review label / 量測數字)"
        ], [])

    created = _parse_ts(created_at)
    resolved_files = []
    for f in anchors["files"]:
        rp = _resolve_file(git, f["path"])
        if rp:
            resolved_files.append({**f, "resolved_path": rp})

    # Rule: review-round label + PR number co-cited in the same file.
    hit = _label_citation_hit(git, anchors["pr_numbers"], anchors["labels"], window)
    if hit:
        lf, ll, label, pr = hit
        cmd = f"git -C <repo> grep -n -F -e '{label}' -- {lf}"
        if _still_open_marked(git, lf, ll):
            return Evidence(
                "CITED_BUT_MARKED_UNFIXED",
                [f"`{label}` 與 `PR #{pr}` 在 {lf}:{ll} 附近同時出現,但周圍文字自陳未修 "
                 "(unbounded / NOT fixed / known trade-off 等標記詞)"],
                [cmd],
            )
        return Evidence(
            "SUSPECT_ALREADY_FIXED",
            [f"`{label}` 與 `PR #{pr}` 在 {lf}:{ll} 附近({window} 行內)同時出現 —— "
             f"符合本 repo 既有的修復引註慣例"],
            [cmd],
        )

    # Rule: a specific measurement number cited in the ticket appears
    # verbatim in a comment — high precision (rare to collide by chance).
    # Guarded the same way: the number may be cited to DESCRIBE the
    # problem ("measured at 80,687 bytes, an unbounded cost") rather than
    # to report a fix — fixture testing caught exactly this case.
    for measure in anchors["measurements"]:
        hits = git.grep_fixed(measure)
        if hits:
            mf, ml = hits[0]
            cmd = f"git -C <repo> grep -n -F -e '{measure}' -- {mf}"
            if _still_open_marked(git, mf, ml):
                return Evidence(
                    "CITED_BUT_MARKED_UNFIXED",
                    [f"量測數字 `{measure}` 出現於 {mf}:{ml},但周圍文字自陳未修"],
                    [cmd],
                )
            return Evidence(
                "SUSPECT_ALREADY_FIXED",
                [f"量測數字 `{measure}` 原字出現於 {mf}:{ml}"],
                [cmd],
            )

    # Rule: non-generic symbol found repo-wide AND its anchor file (if any)
    # was touched at a commit strictly after the ticket was filed. Symbol
    # match alone is too weak (could predate the ticket); recency alone is
    # too weak (one commit can bundle many unrelated findings' fixes) — the
    # AND of both is what fixture testing showed actually discriminates.
    sym_hit = _symbol_citation_hit(git, anchors["symbols"])
    if sym_hit:
        sym, (sf, sl) = sym_hit
        touched_after = False
        checked_file = None
        for rf in resolved_files:
            ts = _parse_ts(git.last_commit_iso(rf["resolved_path"]))
            if ts and created and ts > created:
                touched_after = True
                checked_file = rf["resolved_path"]
                break
        if touched_after or not resolved_files:
            cmd = f"git -C <repo> grep -n -F -e '{sym}'"
            if _still_open_marked(git, sf, sl):
                return Evidence(
                    "CITED_BUT_MARKED_UNFIXED",
                    [f"符號 `{sym}` 存在於 {sf}:{sl},但周圍文字自陳未修"],
                    [cmd],
                )
            # No file:line anchor to cross-check recency against (symbol-
            # only ticket) — still report, but the caller can see from the
            # single reason line that recency wasn't corroborated.
            reasons = [f"符號 `{sym}` 存在於 {sf}:{sl}"]
            if checked_file:
                reasons.append(f"{checked_file} 最後修改晚於單子建立時間")
            return Evidence("SUSPECT_ALREADY_FIXED", reasons, [cmd])

    # Rule: file:line anchor exists, but referenced line is beyond the
    # file's current length — degraded case ①, line drifted.
    for f in anchors["files"]:
        rp = _resolve_file(git, f["path"])
        if not rp or f["line_start"] is None:
            continue
        total = git.line_count(rp)
        if total is not None and f["line_start"] > total:
            return Evidence(
                "ANCHOR_LINE_OUT_OF_RANGE",
                [f"{f['path']}:{f['line_start']} 已超出 {rp} 現有 {total} 行 —— "
                 "檔案可能被大幅重構,行號需要人工重新定位"],
                [f"wc -l <repo>/{rp}"],
            )

    # Rule: a named symbol was extracted but doesn't exist anywhere in the
    # repo by that exact name — degraded case ②, possible rename.
    if anchors["symbols"] and sym_hit is None:
        return Evidence(
            "SYMBOL_NOT_FOUND_POSSIBLY_RENAMED",
            [f"符號 {anchors['symbols']} 在 repo 內找不到任何一個 —— "
             "可能已被改名或移除,需要人工用內容搜尋"],
            [f"git -C <repo> grep -n -F -e '{s}'" for s in anchors["symbols"][:3]],
        )

    # Rule: anchor file exists and is untouched since the ticket was filed
    # — confident negative.
    for rf in resolved_files:
        ts = _parse_ts(git.last_commit_iso(rf["resolved_path"]))
        if ts and created and ts <= created:
            return Evidence(
                "STILL_OPEN_LIKELY",
                [f"{rf['resolved_path']} 自單子建立({created_at})以來沒有任何 commit 動過"],
                [f"git -C <repo> log -1 --format=%cI -- {rf['resolved_path']}"],
            )

    return Evidence("UNKNOWN_NEEDS_HUMAN", [
        "有錨點但證據不足以判定任一方向 —— 人工複驗"
    ], [])
