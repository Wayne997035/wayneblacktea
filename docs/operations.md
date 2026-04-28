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

### Step 2 — substitute the sentinel and apply the migration

Both `migrations/000011_backfill_workspace_id.up.sql` and
`migrations/000011_backfill_workspace_id.down.sql` ship with the
sentinel literal `00000000-0000-0000-0000-000000000000`. Replace it
with `$WORKSPACE_UUID` and run the up migration:

```bash
# Substitute in place. Keep a backup the first time you run this.
sed -i.bak \
  "s/00000000-0000-0000-0000-000000000000/$WORKSPACE_UUID/g" \
  migrations/000011_backfill_workspace_id.up.sql \
  migrations/000011_backfill_workspace_id.down.sql

# Apply against the target Postgres.
psql "$DATABASE_URL" -f migrations/000011_backfill_workspace_id.up.sql
```

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
# Reuses the same substituted .down.sql from Step 2.
psql "$DATABASE_URL" -f migrations/000011_backfill_workspace_id.down.sql
```

This sets `workspace_id = NULL` only on rows that match the sentinel
you applied, so other workspaces are untouched. Then unset
`WORKSPACE_ID` (Railway: delete the variable; local: remove the line
from `.env`) and redeploy.

### SQLite self-hosters

The SQLite backend has no incremental migration tool — `schema.sql`
is applied idempotently at boot. Use the parallel script
[`migrations/sqlite/000011_backfill_workspace_id.up.sql`](../migrations/sqlite/000011_backfill_workspace_id.up.sql)
instead of the Postgres one:

```bash
sed -i.bak \
  "s/00000000-0000-0000-0000-000000000000/$WORKSPACE_UUID/g" \
  migrations/sqlite/000011_backfill_workspace_id.up.sql \
  migrations/sqlite/000011_backfill_workspace_id.down.sql

sqlite3 ./wayneblacktea.db < migrations/sqlite/000011_backfill_workspace_id.up.sql
```

Steps 3 and 4 are identical to the Postgres workflow.
