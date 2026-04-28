# Operations runbook

Production / self-host operational tasks for wayneblacktea. Each
section is a single procedure — read top to bottom, run the commands
in order, verify the post-conditions before moving on.

This file is the canonical place for one-shot ops actions. The
day-to-day "how do I bring the server up" lives in
[`installation.md`](installation.md).

---

## Backfill `workspace_id` (one-time, per environment)

**When to run:** the first time you switch from "no workspace
scoping" to "scoped to a personal workspace UUID". After this point
every previously-NULL row is bound to that workspace and becomes
visible to a server started with `WORKSPACE_ID=<that-uuid>`.

**What it touches:** all 11 domain tables that carry a
`workspace_id` column (`goals`, `projects`, `tasks`, `activity_log`,
`repos`, `decisions`, `session_handoffs`, `knowledge_items`,
`concepts`, `review_schedule`, `pending_proposals`).

**Reversibility:** the down migration only flips rows whose
`workspace_id` matches the sentinel UUID you picked, so it does not
nuke other workspaces' data if you ran the backfill twice with
different sentinels.

### Step 1 — pick a personal workspace UUID

> ⚠️ **DO NOT use the sentinel value `00000000-0000-0000-0000-000000000000`
> as your real `WORKSPACE_ID`.** The sentinel is a placeholder that the
> migration files ship with so `sed` can substitute it for your real
> UUID. It is itself a syntactically valid UUID, so the database will
> happily accept it — but on any shared / forked / template DB every
> backfill will land on the same nil-UUID workspace and you will see
> cross-tenant data co-mingling. Always generate a fresh UUID below.

Generate one and save it. This is the value you will paste into both
the SQL file and the `WORKSPACE_ID` environment variable.

```bash
WORKSPACE_UUID=$(uuidgen | tr '[:upper:]' '[:lower:]')
echo "$WORKSPACE_UUID"
# 6e6f7c40-2e45-4c98-9e2a-4f0a93e0e1aa   ← example output, yours will differ
```

Lowercase is required because Postgres `uuid` literals canonicalise
to lowercase and the application emits lowercase in API responses.
If you skip the `tr` step, `psql` will still accept it but later
string comparisons in tooling may diverge.

Sanity check before you continue — refuse to proceed if your UUID is
the nil sentinel:

```bash
if [ "$WORKSPACE_UUID" = "00000000-0000-0000-0000-000000000000" ]; then
  echo "refusing to use the nil sentinel as WORKSPACE_ID" >&2
  exit 1
fi
```

### Step 2 — substitute the sentinel and apply the migration

Both `migrations/000011_backfill_workspace_id.up.sql` and
`migrations/000011_backfill_workspace_id.down.sql` ship with the
sentinel literal `00000000-0000-0000-0000-000000000000`. **Copy them
to `/tmp/` and run `sed` on the copies** — never edit the files in
the repo. Editing in place would (a) leave the substituted UUID in
your worktree where a careless `git add` could leak it, and (b)
break the sentinel-match safety guarantee on the next run, because a
re-substitution would no longer find the literal `0000…` pattern.

```bash
# Copy migration files to /tmp so the repo copies stay pristine.
cp migrations/000011_backfill_workspace_id.up.sql   /tmp/applied-000011.up.sql
cp migrations/000011_backfill_workspace_id.down.sql /tmp/applied-000011.down.sql

# Substitute the sentinel for your real UUID on the /tmp copies only.
sed -i \
  "s/00000000-0000-0000-0000-000000000000/$WORKSPACE_UUID/g" \
  /tmp/applied-000011.up.sql \
  /tmp/applied-000011.down.sql

# Apply against the target Postgres.
psql "$DATABASE_URL" -f /tmp/applied-000011.up.sql
```

> Note: BSD `sed` (macOS) requires `sed -i ''` instead of `sed -i`.
> Substitute accordingly if you are running on macOS.

Keep `/tmp/applied-000011.down.sql` until you have verified the
backfill (Step 4) — Rollback below reuses it. After verification you
can `rm /tmp/applied-000011.up.sql /tmp/applied-000011.down.sql`.

**Verify** the row count was non-zero:

```bash
psql "$DATABASE_URL" -c \
  "SELECT 'goals' AS t, COUNT(*) FROM goals WHERE workspace_id = '$WORKSPACE_UUID'
   UNION ALL SELECT 'projects', COUNT(*) FROM projects WHERE workspace_id = '$WORKSPACE_UUID'
   UNION ALL SELECT 'tasks', COUNT(*) FROM tasks WHERE workspace_id = '$WORKSPACE_UUID';"
```

If counts are all zero, your DB had no NULL-workspace rows in those
tables — that is fine, and the WORKSPACE_ID env will still scope
future writes.

### Step 3 — set `WORKSPACE_ID` in the runtime environment

**Railway (production):**

1. Open the service in the Railway dashboard.
2. *Variables → New Variable*: `WORKSPACE_ID = <paste $WORKSPACE_UUID>`.
3. Save. (Do *not* commit this UUID to git — it is per-environment.)

**Local self-host:**

```bash
echo "WORKSPACE_ID=$WORKSPACE_UUID" >> .env
```

### Step 4 — redeploy and verify

Railway will redeploy on env var save. For local:

```bash
./bin/wayneblacktea-server -env .env
```

Verify the server picked it up by checking the startup log line
(`workspace scoping: enabled (uuid=…)`) and querying any list
endpoint:

```bash
curl -H "Authorization: Bearer $API_KEY" \
  https://your-host/api/projects | jq '.[] | .workspace_id' | sort -u
# Should print your $WORKSPACE_UUID and nothing else.
```

### Rollback

If you need to undo the backfill (e.g. you picked the wrong UUID):

```bash
# Reuses the substituted /tmp/applied-000011.down.sql from Step 2.
psql "$DATABASE_URL" -f /tmp/applied-000011.down.sql
```

This sets `workspace_id = NULL` only on rows that match the UUID you
applied (it was substituted into the `.down.sql` in Step 2), so
other workspaces are untouched. Then unset `WORKSPACE_ID` (Railway:
delete the variable; local: remove the line from `.env`) and
redeploy.

If `/tmp/applied-000011.down.sql` has been deleted, regenerate it by
re-running the `cp` + `sed` block from Step 2 with the same
`WORKSPACE_UUID` value before applying.

### SQLite self-hosters

The SQLite backend has no incremental migration tool — `schema.sql`
is applied idempotently at boot. Use the parallel script
[`migrations/sqlite/000011_backfill_workspace_id.up.sql`](../migrations/sqlite/000011_backfill_workspace_id.up.sql)
instead of the Postgres one. Same `/tmp` discipline applies — copy
out, substitute on the copy, do not edit the repo files in place:

```bash
cp migrations/sqlite/000011_backfill_workspace_id.up.sql   /tmp/applied-sqlite-000011.up.sql
cp migrations/sqlite/000011_backfill_workspace_id.down.sql /tmp/applied-sqlite-000011.down.sql

sed -i \
  "s/00000000-0000-0000-0000-000000000000/$WORKSPACE_UUID/g" \
  /tmp/applied-sqlite-000011.up.sql \
  /tmp/applied-sqlite-000011.down.sql

sqlite3 ./wayneblacktea.db < /tmp/applied-sqlite-000011.up.sql
```

Rollback (only after you verified the backfill went wrong):

```bash
sqlite3 ./wayneblacktea.db < /tmp/applied-sqlite-000011.down.sql
```

Cleanup once you are happy with Step 4 verification:

```bash
rm /tmp/applied-sqlite-000011.up.sql /tmp/applied-sqlite-000011.down.sql
```

Steps 3 and 4 are identical to the Postgres workflow.
