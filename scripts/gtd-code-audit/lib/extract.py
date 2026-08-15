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

# Every GTD ticket this team files carries a severity/kind tag in its title
# ("🔴 Critical", "🟠 Major", "🟡 Minor", "🔵 Suggestion") — these are plain
# English words that happen to match PASCAL_RE and, being common vocabulary,
# grep-match somewhere in almost any large codebase's comments. Left
# unfiltered, "Minor" alone produced a false SUSPECT_ALREADY_FIXED on a
# ticket with zero relationship to the matched line (found via fixture
# testing: 5d29b5d1 "Minor" matched an unrelated comment in atomizer.go).
# This stoplist is deliberately the tag vocabulary, not a general English
# dictionary — a real Go identifier is never one of these bare words.
_PASCAL_STOPLIST = {
    "Minor", "Major", "Critical", "Blocking", "Suggestion", "Accepted",
    "Round", "Review", "Format", "General", "Feature", "Refactor",
    "Research", "Chore",
}


def _clean(s):
    return CONTROL_CHARS.sub('', s or '')


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

    return {
        "files": files,
        "symbols": sorted(symbols),
        "labels": sorted(set(LABEL_RE.findall(text))),
        "pr_numbers": sorted(set(PR_RE.findall(text))),
        "commit_shas": sorted(set(COMMIT_RE.findall(text))),
        "measurements": sorted(set(MEASURE_RE.findall(text))),
        "test_names": sorted(set(TEST_NAME_RE.findall(text))),
        "acceptance_text": accept_text,
    }


def has_any_anchor(anchors: dict) -> bool:
    return bool(
        anchors["files"] or anchors["symbols"] or anchors["commit_shas"]
        or anchors["labels"] or anchors["measurements"] or anchors["test_names"]
    )
