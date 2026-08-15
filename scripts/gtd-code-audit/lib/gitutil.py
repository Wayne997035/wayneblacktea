"""gitutil — the ONLY place anchor strings extracted from ticket text touch
the filesystem or a subprocess.

Threat model (backend-security-design.md §2.2, §2.3): a GTD ticket's title/
description is writable by any agent, including a prompt-injected one. Every
string that reaches this module ultimately came from that untrusted text.
Two rules make that safe:

  1. subprocess is ALWAYS called with an argv list, NEVER shell=True and
     NEVER an interpolated shell string. `git grep -F -e <literal>` treats
     <literal> as inert bytes to search for, not shell syntax — a payload
     like `$(rm -rf ~)` just fails to match anything.
  2. Every path is resolved with `safe_path` before touching disk: reject
     null bytes/CR/LF, reject leading `~`, resolve symlinks, and verify the
     resolved absolute path is still inside repo_root (the `+sep` guard
     blocks sibling-prefix escapes like `/repohijack` matching `/repo`).
"""
import os
import re
import subprocess
from pathlib import Path

HEX_SHA_RE = re.compile(r'^[0-9a-f]{7,40}$')
_UNSAFE_PATH_CHARS = re.compile(r'[\x00\r\n]')


def safe_path(repo_root: Path, rel_path: str) -> Path | None:
    """Resolve rel_path against repo_root. Returns None if it's unsafe or
    escapes repo_root — callers MUST treat None as "cannot check this
    anchor", never fall back to a less-safe resolution."""
    if not rel_path or _UNSAFE_PATH_CHARS.search(rel_path):
        return None
    if rel_path.startswith('~') or rel_path.startswith('/') or '\x00' in rel_path:
        return None
    root = repo_root.resolve()
    try:
        # Reject symlink components pointing outside root the same way as
        # a normal Clean+EvalSymlinks check: resolve() follows symlinks,
        # then we verify containment on the RESOLVED path, not the literal
        # one — a symlink inside repo_root pointing outside it is caught
        # here even though the literal path string looked fine.
        resolved = (root / rel_path).resolve()
    except (OSError, RuntimeError):
        return None
    if resolved == root or str(resolved).startswith(str(root) + os.sep):
        return resolved
    return None


class GitOps:
    """Thin, testable wrapper around read-only git subprocess calls."""

    def __init__(self, repo_root: Path, timeout: int = 20):
        self.repo_root = Path(repo_root)
        self.timeout = timeout
        self._ls_files_cache = None

    def _run(self, args: list[str]):
        try:
            # Explicit noqa below: argv list, never shell=True — every element
            # (including anything derived from ticket text via the callers
            # in rules.py) is passed as an inert argv entry, not shell text.
            # "git" resolves via PATH deliberately (same as scan-review.py's
            # convention elsewhere in this repo).
            r = subprocess.run(  # noqa: S603
                ["git", "-C", str(self.repo_root), *args],  # noqa: S607
                capture_output=True, text=True, timeout=self.timeout,
            )
            return r.returncode, r.stdout, r.stderr
        except Exception as e:  # noqa: BLE001 — caller only needs "it failed"
            return 1, "", str(e)

    def file_exists(self, rel_path: str) -> bool:
        p = safe_path(self.repo_root, rel_path)
        return p is not None and p.is_file()

    def line_count(self, rel_path: str) -> int | None:
        p = safe_path(self.repo_root, rel_path)
        if p is None or not p.is_file():
            return None
        try:
            with p.open('r', encoding='utf-8', errors='ignore') as f:
                return sum(1 for _ in f)
        except OSError:
            return None

    def read_context(self, rel_path: str, line: int, radius: int = 3) -> str:
        """Return the text of lines [line-radius, line+radius] of rel_path,
        1-indexed, clamped to the file's bounds. Used to check whether a
        citation match sits next to language marking it as still-unfixed
        ('unbounded', 'NOT fixed by this change', ...) rather than to
        interpret code semantics generally — see rules.py."""
        p = safe_path(self.repo_root, rel_path)
        if p is None or not p.is_file() or line < 1:
            return ""
        try:
            lines = p.read_text(encoding='utf-8', errors='ignore').splitlines()
        except OSError:
            return ""
        lo = max(0, line - 1 - radius)
        hi = min(len(lines), line + radius)
        return "\n".join(lines[lo:hi])

    def last_commit_iso(self, rel_path: str) -> str | None:
        """ISO-8601 commit date of the most recent commit touching rel_path."""
        if safe_path(self.repo_root, rel_path) is None:
            return None
        rc, out, _ = self._run(["log", "-1", "--format=%cI", "--", rel_path])
        out = out.strip()
        return out if rc == 0 and out else None

    def resolve_basename(self, basename: str) -> str | None:
        """Ticket text often drops the directory prefix ('context_render.go'
        instead of 'internal/cli/context_render.go'). Find the unique
        tracked file ending in /<basename>; ambiguous or zero matches
        return None rather than guessing (same policy as scan-review.py's
        _resolve — dangerous to silently pick wrong file in a security
        audit tool)."""
        if self._ls_files_cache is None:
            rc, out, _ = self._run(["ls-files"])
            self._ls_files_cache = out.splitlines() if rc == 0 else []
        matches = [f for f in self._ls_files_cache
                   if f == basename or f.endswith("/" + basename)]
        return matches[0] if len(matches) == 1 else None

    def grep_fixed(self, literal: str) -> list[tuple[str, int]]:
        """git grep -n -F -e <literal> — fixed-string, case-sensitive,
        repo-wide. literal is one argv element; shell metacharacters in it
        are inert. Overlong or empty literals are refused (also guards
        against accidentally grepping for a whole paragraph)."""
        if not literal or len(literal) > 200:
            return []
        rc, out, _ = self._run(["grep", "-n", "-F", "-e", literal])
        if rc not in (0, 1):  # 1 == "ran fine, zero matches"
            return []
        hits = []
        for line in out.splitlines():
            m = re.match(r'^([^:]+):(\d+):', line)
            if m:
                hits.append((m.group(1), int(m.group(2))))
        return hits

    def object_exists(self, sha: str) -> bool:
        if not HEX_SHA_RE.match(sha):
            return False
        rc, _, _ = self._run(["cat-file", "-e", sha])
        return rc == 0
