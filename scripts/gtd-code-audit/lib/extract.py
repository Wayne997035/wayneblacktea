"""extract — pull machine-checkable anchors out of a GTD ticket's title +
description.

GTD ticket text is written by whoever/whatever files the ticket — including
an agent that could be prompt-injected. Every regex here is used ONLY for
literal string matching downstream (grep -F, path existence, integer
comparison) — never for shell interpolation or eval. See lib/gitutil.py for
where the actual boundary is enforced.

Nothing in this module touches the filesystem or runs a subprocess; it is
pure text-in, dict-out, which is what makes it unit-testable without a repo.
"""
import re

# file:line or file:line-line. Extension list matches this monorepo's stacks
# (Go backend, SQL migrations, Python/Node tooling, shell, YAML/Taskfile,
# docs). Extend the list rather than loosening the character class — a bare
# `[\w./-]+\.\w+` would also match version strings like "v1.50.0".
FILE_LINE_RE = re.compile(
    r'(?<![\w/.])((?:[A-Za-z0-9_.-]+/)*[A-Za-z0-9_-]+\.'
    r'(?:go|sql|py|ts|tsx|js|mjs|sh|yml|yaml|md))'
    r'(?::(\d+)(?:-(\d+))?)?(?![\w])'
)

# mixedCase (checkCommandField, newHookPgxPool) and PascalCase
# (NextActionsTotal, OpenReadOnly) Go-style identifiers. PascalCase requires
# a lowercase second char so ALL-CAPS acronyms (API, GTD, SQL, PR) don't
# spuriously match.
CAMEL_RE = re.compile(r'\b[a-z][a-zA-Z0-9]*[A-Z][a-zA-Z0-9]{2,}\b')
PASCAL_RE = re.compile(r'\b[A-Z][a-z][A-Za-z0-9]{3,}\b')
# snake_case (repo_name, file_map, next_actions_total) and kebab-case
# (untrusted-local-db). Minimum length filters out noise like "e_g".
SNAKE_RE = re.compile(r'\b[a-z][a-z0-9]*(?:_[a-z0-9]+){1,}\b')
KEBAB_RE = re.compile(r'\b[a-z][a-z0-9]*(?:-[a-z0-9]+){1,}\b')

# Review-round finding codes this team's GTD tickets use in titles/bodies:
# "M-4", "C-1", "m-R4", "M-R9", "m-3". Case carries no reliable signal here
# (both "M-3" and "m-3" show up), so both are accepted.
LABEL_RE = re.compile(r'\b([Mm]-R?\d{1,3}|C-R?\d{1,3})\b')
PR_RE = re.compile(r'PR\s*#\s*(\d+)', re.I)
# Only treat a hex token as a commit SHA if a cue word precedes it — a bare
# 7-hex-char run is common coincidental noise (ids, hashes, timestamps).
COMMIT_RE = re.compile(r'(?:base\s+)?commit[:\s]+([0-9a-f]{7,40})\b', re.I)
# Comma-grouped numbers ("80,687", "8,300") are high-precision measurement
# citations — rare enough that a match is meaningful, unlike bare digits.
MEASURE_RE = re.compile(r'\b\d{1,3}(?:,\d{3})+\b')
TEST_NAME_RE = re.compile(r'\bTest[A-Za-z0-9_]{3,}\b')
ACCEPT_RE = re.compile(r'acceptance[:：]', re.I)

# C0 control chars minus \t/\n (already newline-safe since we operate on
# whole fields) — strip before any downstream use, matching
# backend-security-design.md §5.4's audit-text sanitisation pattern.
CONTROL_CHARS = re.compile(r'[\x00-\x08\x0b\x0c\x0e-\x1f]')

MIN_SYMBOL_LEN = 5
_SYMBOL_STOPLIST = {"e_g", "i_e", "vs_the"}

# Anchor-count cap per category (DoS guard — GTD ticket description is an
# unbounded TEXT column with no length cap at the add_task MCP tool layer
# either; backend-security-design.md §2.1 treats this input as adversarial).
# Every anchor that clears extraction potentially costs one `git grep`
# subprocess call downstream in lib/rules.py (~0.12-0.13s measured locally,
# subprocess.run + git process spin-up dominates). Without a cap, a ticket
# description padded with N unique symbol-shaped tokens costs O(N) calls in
# the worst case (rules.py's _symbol_citation_hit tries every symbol until
# a hit) — measured directly: 300 unique non-matching tokens took 37.1s
# end-to-end (~0.124s/token), so an unbounded ~40,000-token payload
# extrapolates to over an hour of subprocess time for a single ticket.
# Real-world ceiling, measured against all 16 tickets in
# testdata/known_tickets.json: the densest real ticket extracts 18 symbols
# (0 for labels/pr_numbers beyond low single digits). MAX_ANCHORS_PER_CATEGORY
# = 50 keeps >2.7x headroom over that real-world max while bounding worst-
# case subprocess calls per ticket to a low hundreds, not tens of thousands
# (see lib/rules.py's _label_citation_hit for why labels/pr_numbers also
# need grep_fixed's memoization on top of this cap, not just this cap
# alone — the pr_numbers × labels nested loop multiplies rather than sums).
MAX_ANCHORS_PER_CATEGORY = 50

# Every GTD ticket this team files carries a severity/kind tag in its title
# ("🔴 Critical", "🟠 Major", "🟡 Minor", "🔵 Suggestion") — these are plain
# English words that happen to match PASCAL_RE and, being common vocabulary,
# grep-match somewhere in almost any large codebase's comments. Left
# unfiltered, "Minor" alone produced a false SUSPECT_ALREADY_FIXED on a
# ticket with zero relationship to the matched line (found via fixture
# testing: 61c67e15 "Minor" matched an unrelated comment in atomizer.go).
# This stoplist is deliberately the tag vocabulary, not a general English
# dictionary — a real Go identifier is never one of these bare words.
_PASCAL_STOPLIST = {
    "Minor", "Major", "Critical", "Blocking", "Suggestion", "Accepted",
    "Round", "Review", "Format", "General", "Feature", "Refactor",
    "Research", "Chore",
}


def _clean(s):
    return CONTROL_CHARS.sub('', s or '')


def _cap(items: list, limit: int = MAX_ANCHORS_PER_CATEGORY) -> tuple[list, int]:
    """Truncate to `limit`, returning (kept, dropped_count). Callers MUST
    surface a non-zero dropped_count to the human/agent consuming the
    verdict — see extract_anchors()'s "truncated" key and
    rules.classify()'s prepended reason line. A cap that silently drops
    anchors with no visible trace is exactly what this tool's own README
    forbids ("no silent caps")."""
    if len(items) <= limit:
        return items, 0
    return items[:limit], len(items) - limit


def extract_anchors(title: str, description: str) -> dict:
    """Pure function: ticket text -> structured anchors. No I/O."""
    title = _clean(title)
    description = _clean(description)
    text = f"{title}\n{description}"

    files = []
    seen = set()
    for m in FILE_LINE_RE.finditer(text):
        path, ln1, ln2 = m.group(1), m.group(2), m.group(3)
        key = (path, ln1, ln2)
        if key in seen:
            continue
        seen.add(key)
        files.append({
            "path": path,
            "line_start": int(ln1) if ln1 else None,
            "line_end": int(ln2) if ln2 else (int(ln1) if ln1 else None),
        })

    symbols = set()
    symbols |= {s for s in CAMEL_RE.findall(text) if len(s) >= MIN_SYMBOL_LEN}
    symbols |= {s for s in PASCAL_RE.findall(text)
                if len(s) >= MIN_SYMBOL_LEN and s not in _PASCAL_STOPLIST}
    for rx in (SNAKE_RE, KEBAB_RE):
        symbols |= {
            m.group(0) for m in rx.finditer(text)
            if len(m.group(0)) >= MIN_SYMBOL_LEN and m.group(0) not in _SYMBOL_STOPLIST
        }
    # A symbol that's just a file's own basename (minus extension) is
    # redundant with the file anchor and grepping it repo-wide is near-
    # certain to just re-find the file path itself everywhere it's
    # imported — drop those to avoid manufacturing false corroboration.
    file_stems = {f["path"].rsplit("/", 1)[-1].rsplit(".", 1)[0] for f in files}
    symbols = {s for s in symbols if s not in file_stems}

    accept_text = None
    am = ACCEPT_RE.search(description)
    if am:
        accept_text = description[am.end():].strip()

    # Cap every category (DoS guard, see MAX_ANCHORS_PER_CATEGORY above).
    # symbols is capped longest-first (not alphabetically) BEFORE the final
    # sort — a longer Go-shaped token is more specific/higher-signal per
    # lib/rules.py::_symbol_citation_hit's own stated rationale, so if
    # anything has to be dropped it should be the short, generic-sounding
    # tail, not whatever happens to sort last alphabetically.
    symbols_by_len = sorted(symbols, key=len, reverse=True)
    symbols_kept, symbols_dropped = _cap(symbols_by_len)
    files_kept, files_dropped = _cap(files)
    labels_kept, labels_dropped = _cap(sorted(set(LABEL_RE.findall(text))))
    pr_kept, pr_dropped = _cap(sorted(set(PR_RE.findall(text))))
    sha_kept, sha_dropped = _cap(sorted(set(COMMIT_RE.findall(text))))
    measure_kept, measure_dropped = _cap(sorted(set(MEASURE_RE.findall(text))))
    test_kept, test_dropped = _cap(sorted(set(TEST_NAME_RE.findall(text))))

    truncated = {
        cat: dropped
        for cat, dropped in (
            ("files", files_dropped), ("symbols", symbols_dropped),
            ("labels", labels_dropped), ("pr_numbers", pr_dropped),
            ("commit_shas", sha_dropped), ("measurements", measure_dropped),
            ("test_names", test_dropped),
        )
        if dropped
    }

    return {
        "files": files_kept,
        "symbols": sorted(symbols_kept),
        "labels": labels_kept,
        "pr_numbers": pr_kept,
        "commit_shas": sha_kept,
        "measurements": measure_kept,
        "test_names": test_kept,
        "acceptance_text": accept_text,
        "truncated": truncated,
    }


def has_any_anchor(anchors: dict) -> bool:
    return bool(
        anchors["files"] or anchors["symbols"] or anchors["commit_shas"]
        or anchors["labels"] or anchors["measurements"] or anchors["test_names"]
    )
