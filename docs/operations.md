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

---

## WORKSPACE_ID Backfill (migration 000015)

Migration `000015_workspace_id_backfill` is the production-ready follow-up to
the scaffold in `000011`. Run this if you are enabling workspace scoping on a
production DB that was not covered by `000011` (e.g. Railway instances where
`000011` was skipped because it was marked NOT AUTO-RUN).

**Sentinel UUID used by 000015:** `00000000-0000-0000-0000-000000000001`
(distinct from the `000011` sentinel `…000000000000` so down.sql can target
exactly the rows this migration set, not rows from a previous 000011 run).

**Tables covered:** `goals`, `projects`, `tasks`, `activity_log`, `repos`,
`decisions`, `session_handoffs`, `knowledge_items`, `concepts`,
`review_schedule`, `pending_proposals`.

Tables added after 000011 (`workspace_preferences`, `project_arch`) are
intentionally excluded: `workspace_preferences` uses `workspace_id` as PRIMARY
KEY (never NULL), and `project_arch` is tenant-agnostic (slug-keyed).

### Step 0 — dry-run to see how many rows will be touched

```bash
psql "$DATABASE_URL" -c "
  SELECT 'goals'             AS t, COUNT(*) FROM goals             WHERE workspace_id IS NULL
  UNION ALL SELECT 'projects',     COUNT(*) FROM projects          WHERE workspace_id IS NULL
  UNION ALL SELECT 'tasks',        COUNT(*) FROM tasks             WHERE workspace_id IS NULL
  UNION ALL SELECT 'activity_log', COUNT(*) FROM activity_log      WHERE workspace_id IS NULL
  UNION ALL SELECT 'repos',        COUNT(*) FROM repos             WHERE workspace_id IS NULL
  UNION ALL SELECT 'decisions',    COUNT(*) FROM decisions         WHERE workspace_id IS NULL
  UNION ALL SELECT 'session_handoffs', COUNT(*) FROM session_handoffs WHERE workspace_id IS NULL
  UNION ALL SELECT 'knowledge_items',  COUNT(*) FROM knowledge_items  WHERE workspace_id IS NULL
  UNION ALL SELECT 'concepts',     COUNT(*) FROM concepts          WHERE workspace_id IS NULL
  UNION ALL SELECT 'review_schedule',  COUNT(*) FROM review_schedule  WHERE workspace_id IS NULL
  UNION ALL SELECT 'pending_proposals', COUNT(*) FROM pending_proposals WHERE workspace_id IS NULL;
"
```

If all counts are 0 you do not need this migration — every row already has a
workspace assigned.

### Step 1 — pick your WORKSPACE_ID

Follow the same steps as the 000011 section above to generate and validate a
UUID. If you already ran 000011 and set WORKSPACE_ID in Railway, reuse that
same UUID here so all rows end up in the same workspace.

```bash
WORKSPACE_UUID=$(uuidgen | tr '[:upper:]' '[:lower:]')
echo "$WORKSPACE_UUID"

# Sanity check — refuse the nil sentinel and 000011's sentinel
if [ "$WORKSPACE_UUID" = "00000000-0000-0000-0000-000000000000" ] || \
   [ "$WORKSPACE_UUID" = "00000000-0000-0000-0000-000000000001" ]; then
  echo "refusing to use a reserved sentinel as WORKSPACE_ID" >&2
  exit 1
fi
```

### Step 2 — substitute sentinel and apply (Postgres)

```bash
# Copy to /tmp — NEVER edit the repo files in place.
cp migrations/000015_workspace_id_backfill.up.sql   /tmp/applied-000015.up.sql
cp migrations/000015_workspace_id_backfill.down.sql /tmp/applied-000015.down.sql

# Substitute sentinel with your real UUID (BSD sed on macOS: sed -i '').
sed -i "s/00000000-0000-0000-0000-000000000001/$WORKSPACE_UUID/g" \
    /tmp/applied-000015.up.sql \
    /tmp/applied-000015.down.sql

# Apply.
psql "$DATABASE_URL" -f /tmp/applied-000015.up.sql
```

**Verify** the backfill landed:

```bash
psql "$DATABASE_URL" -c \
  "SELECT 'goals' AS t, COUNT(*) FROM goals WHERE workspace_id = '$WORKSPACE_UUID'
   UNION ALL SELECT 'tasks', COUNT(*) FROM tasks WHERE workspace_id = '$WORKSPACE_UUID'
   UNION ALL SELECT 'decisions', COUNT(*) FROM decisions WHERE workspace_id = '$WORKSPACE_UUID';"
```

### Step 3 — set WORKSPACE_ID in Railway

1. Open the service in the Railway dashboard.
2. **Variables** -> **New Variable**: `WORKSPACE_ID = <paste $WORKSPACE_UUID>`.
3. Save. Railway will redeploy automatically.

Do NOT commit the UUID to git — it is per-environment sensitive configuration.

Verify the server picked it up:

```bash
railway logs --tail | grep "workspace scoping"
# Expected: workspace scoping: enabled (uuid=<your-uuid>)
```

### Rollback

```bash
# Reuses the /tmp copy with your UUID already substituted.
psql "$DATABASE_URL" -f /tmp/applied-000015.down.sql
```

Then remove `WORKSPACE_ID` from Railway variables and redeploy.

### SQLite self-hosters

```bash
cp migrations/sqlite/000015_workspace_id_backfill.up.sql   /tmp/applied-sqlite-000015.up.sql
cp migrations/sqlite/000015_workspace_id_backfill.down.sql /tmp/applied-sqlite-000015.down.sql

# BSD sed on macOS: sed -i ''
sed -i "s/00000000-0000-0000-0000-000000000001/$WORKSPACE_UUID/g" \
    /tmp/applied-sqlite-000015.up.sql \
    /tmp/applied-sqlite-000015.down.sql

sqlite3 ./wayneblacktea.db < /tmp/applied-sqlite-000015.up.sql
```

Rollback:

```bash
sqlite3 ./wayneblacktea.db < /tmp/applied-sqlite-000015.down.sql
```

Cleanup after successful verification:

```bash
rm /tmp/applied-000015.up.sql /tmp/applied-000015.down.sql
rm /tmp/applied-sqlite-000015.up.sql /tmp/applied-sqlite-000015.down.sql
```
