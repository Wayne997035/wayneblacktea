"""db — read-only fetch of GTD tasks via psql.

This module issues SELECT only — there is no UPDATE/DELETE path anywhere in
this package, by design (Lead's mandate: the audit script never closes a
ticket, only triages). Credential handling follows
backend-security-design.md §4: env var first, `.env.local` fallback, and the
fallback file's permissions are checked (§4.1) before use.

CLI-supplied `status` and `ids` are validated against explicit allowlists
(§5.2) before being spliced into the SQL text — they are operator-provided
flags, not ticket body content, but validated all the same rather than
trusted blindly.
"""
import csv
import io
import os
import re
import subprocess

import sys
from pathlib import Path

CSV_COLUMNS = ["id", "status", "kind", "priority", "title", "description",
               "created_at", "updated_at"]
ALLOWED_STATUSES = {"pending", "in_progress", "completed", "cancelled"}
UUID_RE = re.compile(
    r'^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$')



_DSN_CRED = re.compile(r"(?<=://)([^:/@\s]+):([^@/\s]+)(?=@)")


def mask_dsn(text):
    """把任何 `scheme://user:password@host` 的密碼換成 `***`。

    **NEVER 只遮我們自己組的那一份字串。** 實際外洩的路徑是例外物件:
    `subprocess.TimeoutExpired` / `CalledProcessError` 的 `str()` 會把**整個 argv**
    印出來,而 DSN 就是其中一個元素 —— 於是密碼從 `RuntimeError(f"...{e}")` 這條路
    進到 stdout、進到 session transcript、再被複製進報告與 handoff。
    遮在訊息組裝的那一刻,才涵蓋得到所有例外型別。
    """
    return _DSN_CRED.sub(r"\1:***", str(text))


def _load_env_local(env_path: Path) -> dict:
    if not env_path.is_file():
        return {}
    info = env_path.stat()
    if info.st_mode & 0o077:
        print(f"WARN: {env_path} is group/world readable "
              f"(mode {oct(info.st_mode & 0o777)}) — recommend chmod 0600",
              file=sys.stderr)
    out = {}
    for line in env_path.read_text(encoding="utf-8", errors="ignore").splitlines():
        line = line.strip()
        if not line or line.startswith("#") or "=" not in line:
            continue
        k, _, v = line.partition("=")
        out[k.strip()] = v.strip()
    return out


def _resolve_credentials(env_local: Path) -> tuple[str, str]:
    dsn = os.environ.get("DATABASE_URL")
    sslrootcert = os.environ.get("PGSSLROOTCERT")
    if not dsn or not sslrootcert:
        loaded = _load_env_local(env_local)
        dsn = dsn or loaded.get("DATABASE_URL")
        sslrootcert = sslrootcert or loaded.get("PGSSLROOTCERT")
    if not dsn or not sslrootcert:
        raise RuntimeError(
            "缺 DATABASE_URL 或 PGSSLROOTCERT(process env 和 "
            f"{env_local} 都沒有) —— 見 wayneblacktea/CLAUDE.md 的 psql 連線段")
    return dsn, sslrootcert


def fetch_tasks(env_local: Path, status: str = "pending",
                 ids: list[str] | None = None, timeout: int = 30) -> list[dict]:
    """Read-only SELECT against the tasks table. Returns a list of dicts
    keyed by CSV_COLUMNS. Raises RuntimeError on any failure — callers
    should not silently proceed with an empty result on error."""
    dsn, sslrootcert = _resolve_credentials(env_local)

    if ids:
        bad = [i for i in ids if not UUID_RE.match(i)]
        if bad:
            raise ValueError(f"--ids 含非 UUID 格式的值: {bad}")
        id_list = ",".join(f"'{i}'" for i in ids)  # each element regex-verified above
        where = f"id::text = ANY(ARRAY[{id_list}])"
    else:
        if status not in ALLOWED_STATUSES:
            raise ValueError(f"--status 必須是 {sorted(ALLOWED_STATUSES)} 之一,收到: {status!r}")
        where = f"status = '{status}'"  # status is allowlist-checked above

    # Explicit noqa below, not silently suppressed: `where` is built only from
    # values already validated above against a fixed allowlist (status) or
    # a strict UUID regex (ids) — never from ticket body text or any other
    # untrusted source. ruff's bandit-style S608 can't see that validation
    # happened a few lines up, hence the explicit reason here.
    query = (
        f"SELECT {', '.join(CSV_COLUMNS)} FROM tasks WHERE {where} "  # noqa: S608
        "ORDER BY created_at DESC"
    )
    env = {**os.environ, "PGSSLROOTCERT": sslrootcert}
    try:
        # Explicit noqa below: argv list (never shell=True), so `query`/`dsn`
        # reaching psql as a single argv element is inert to shell
        # metacharacters — S603/S607 fire on any subprocess call generically
        # and don't distinguish this from an actually-dangerous shell=True
        # invocation. "psql" is resolved via PATH; pinning an absolute path
        # would just break on machines where it's installed elsewhere.
        r = subprocess.run(  # noqa: S603
            ["psql", dsn, "--csv", "-c", query],  # noqa: S607
            capture_output=True, text=True, timeout=timeout, env=env,
        )
    except Exception as e:  # noqa: BLE001
        raise RuntimeError(f"psql 執行失敗: {mask_dsn(e)}") from e
    if r.returncode != 0:
        raise RuntimeError(
            f"psql 回傳非 0(exit {r.returncode}): {mask_dsn(r.stderr.strip())}")

    reader = csv.DictReader(io.StringIO(r.stdout))
    return [dict(row) for row in reader]
