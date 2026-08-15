#!/usr/bin/env python3
"""gtd-code-audit — triage GTD tickets that claim a problem against what
master's code currently looks like, and flag ones that are suspiciously
likely to already be fixed.

Why this exists (not hypothetical — happened twice in one day, 2026-08-15):
PR #157's review findings opened 11 GTD tickets; all 11 fixes landed in the
single merged commit, none of the 11 tickets got closed. Separately, PR
#151's review findings opened 4 tickets; all 4 fixes landed in one merge
commit, none got closed. In the second case nobody checked before
dispatching — 3 engineers opened 3 worktrees to "fix" code that was already
fixed, and produced zero lines of real work. This script is the check that
should have run before that dispatch.

What it is NOT: a judge. "Is this ticket actually resolved" is a semantic
question a human or a reviewing agent has to answer — grep can tell you a
symbol exists, it cannot tell you the fix is correct. This script emits a
graded list of suspects, each with a re-runnable verification command. It
NEVER writes to the tasks table and NEVER auto-closes anything.

Usage:
    python3 audit.py --status pending                # scan all pending tickets
    python3 audit.py --ids <uuid>,<uuid>              # check specific tickets
    python3 audit.py --tasks-json testdata/x.json     # offline mode, no DB
    python3 audit.py --json                           # machine-readable output

Every anchor extracted from ticket text is used ONLY as a literal string in
`git grep -F` (argv list, never shell=True) or as a path resolved and
containment-checked against repo_root before any filesystem access — see
lib/gitutil.py's module docstring for the full threat model. Ticket text is
adversarial input by design; treat it that way when extending this script.
"""
import argparse
import json
import sys
from datetime import date
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parent))
from lib import db as db_mod  # noqa: E402
from lib import rules  # noqa: E402
from lib.gitutil import GitOps  # noqa: E402

HERE = Path(__file__).parent
REPORTS = HERE / "reports"

VERDICT_ORDER = [
    "SUSPECT_ALREADY_FIXED",
    "CITED_BUT_MARKED_UNFIXED",
    "ANCHOR_LINE_OUT_OF_RANGE",
    "SYMBOL_NOT_FOUND_POSSIBLY_RENAMED",
    "UNKNOWN_NEEDS_HUMAN",
    "STILL_OPEN_LIKELY",
    "NO_ANCHOR_NEEDS_HUMAN",
]


def find_repo_root(start: Path) -> Path:
    """The wayneblacktea checkout this script audits against — defaults to
    two levels up from scripts/gtd-code-audit/ (i.e. the repo containing
    this script), NOT the worktree cwd might be sitting in, since the
    question is always "what does master look like", and --repo lets a
    caller override for a different checkout."""
    cur = start.resolve()
    for d in (cur, *cur.parents):
        if (d / ".git").exists():
            return d
    return start


def run(tasks: list[dict], repo_root: Path, window: int) -> list[dict]:
    git = GitOps(repo_root)
    rows = []
    for t in tasks:
        ev = rules.classify(t["title"], t["description"], t.get("created_at", ""),
                             git, window=window)
        rows.append({
            "id": t["id"],
            "status": t.get("status", ""),
            "title": t["title"],
            "verdict": ev.verdict,
            "reasons": ev.reasons,
            "verify_commands": ev.verify_commands,
        })
    return rows


def render_text(rows: list[dict]) -> str:
    out = []
    by_verdict = {v: [r for r in rows if r["verdict"] == v] for v in VERDICT_ORDER}
    out.append(f"gtd-code-audit — {len(rows)} 張單\n")
    for v in VERDICT_ORDER:
        group = by_verdict[v]
        if not group:
            continue
        out.append(f"## {v} ({len(group)})\n")
        for r in group:
            out.append(f"- [{r['id'][:8]}] ({r['status']}) {r['title'][:90]}")
            for reason in r["reasons"]:
                out.append(f"    理由: {reason}")
            for cmd in r["verify_commands"]:
                out.append(f"    驗證: {cmd}")
        out.append("")
    return "\n".join(out)


def main():
    ap = argparse.ArgumentParser(description=__doc__,
                                  formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--repo", default=None, help="要對比的 repo 路徑(預設:本腳本所在 repo 的根)")
    ap.add_argument("--env-local", default=None,
                     help="DB 連線資訊的 .env.local(預設 <repo>/.env.local)")
    ap.add_argument("--status", default="pending", help="DB 篩選狀態(預設 pending)")
    ap.add_argument("--ids", default=None, help="逗號分隔的 task UUID,忽略 --status")
    ap.add_argument("--tasks-json", default=None,
                     help="離線模式:從 JSON 檔讀 tasks(略過 DB),"
                          "欄位需含 id/title/description/created_at/status")
    ap.add_argument("--window", type=int, default=rules.DEFAULT_WINDOW,
                     help="label/PR 引註比對的行數窗口(預設 80)")
    ap.add_argument("--json", action="store_true", help="輸出 JSON 而非文字報告")
    ap.add_argument("--no-write", action="store_true", help="只印,不落檔到 reports/")
    a = ap.parse_args()

    repo_root = Path(a.repo).resolve() if a.repo else find_repo_root(HERE)
    if not (repo_root / ".git").exists():
        print(f"錯誤: {repo_root} 不是 git repo 根目錄(--repo 指過去)", file=sys.stderr)
        return 2

    if a.tasks_json:
        tasks = json.loads(Path(a.tasks_json).read_text(encoding="utf-8"))
    else:
        env_local = Path(a.env_local) if a.env_local else repo_root / ".env.local"
        ids = [i.strip() for i in a.ids.split(",")] if a.ids else None
        try:
            tasks = db_mod.fetch_tasks(env_local, status=a.status, ids=ids)
        except (RuntimeError, ValueError) as e:
            print(f"錯誤: {e}", file=sys.stderr)
            return 2

    rows = run(tasks, repo_root, a.window)

    if a.json:
        text = json.dumps(rows, ensure_ascii=False, indent=2)
    else:
        text = render_text(rows)
    print(text)

    if not a.no_write:
        REPORTS.mkdir(parents=True, exist_ok=True)
        stamp = date.today().isoformat()
        ext = "json" if a.json else "md"
        out_path = REPORTS / f"{stamp}-audit.{ext}"
        out_path.write_text(text, encoding="utf-8")
        print(f"\n(報告已寫入 {out_path})", file=sys.stderr)

    n_suspect = sum(1 for r in rows if r["verdict"] == "SUSPECT_ALREADY_FIXED")
    if n_suspect:
        print(f"\n{n_suspect} 張單疑似已修 —— 這支腳本不會關單,"
              "請人工用附帶的驗證指令複驗後再決定要不要 complete_task。", file=sys.stderr)
    return 0


if __name__ == "__main__":
    sys.exit(main())
