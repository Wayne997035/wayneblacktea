# Operations runbook

Production / self-host operational tasks for wayneblacktea. Each
section is a single procedure — read top to bottom, run the commands
in order, verify the post-conditions before moving on.

This file is the canonical place for one-shot ops actions. The
day-to-day "how do I bring the server up" lives in
[`install.md`](install.md).

---

## Backfill `workspace_id` — legacy migration 000011 (historical)

**Status:** `000011_backfill_workspace_id` was never a working golang-migrate
migration. The original `.up.sql`/`.down.sql` used psql `\set` metacommands,
which golang-migrate cannot parse — fresh-DB spinup, DR replay, and `task
migrate-up` would either fail outright or silently no-op against the broken
files (the test suite carried a `skipMigrations` map to work around it). The
originals have been moved out of the embedded `migrations/` tree entirely to
[`scripts/manual/000011_backfill_workspace_id.psql`](../scripts/manual/000011_backfill_workspace_id.psql),
which is run manually with `psql`. A no-op marker
([`migrations/000036_legacy_011_marker.up.sql`](../migrations/000036_legacy_011_marker.up.sql))
keeps the historical `schema_migrations` row 000011 consistent for databases
that already applied the original, and must remain a no-op forever.

**Recommended path for new installs:** use "WORKSPACE_ID Backfill (migration
000015)" below instead. 000015 covers the same 11 tables with plain SQL (no
psql metacommands) and ships a proper `.down.sql`. Only follow the procedure
below if you specifically need to replay the legacy 000011 script — e.g.
reconciling a database that already has schema_migrations row 000011
applied, or investigating pre-000015 history.

**What it touches:** all 11 domain tables that carry a `workspace_id`
column (`goals`, `projects`, `tasks`, `activity_log`, `repos`, `decisions`,
`session_handoffs`, `knowledge_items`, `concepts`, `review_schedule`,
`pending_proposals`).

**Reversibility:** unlike 000015, the legacy script ships no `.down.sql`.
Undoing it means writing the inverse `UPDATE` by hand — see Rollback below.

### Step 1 — pick a personal workspace UUID

> ⚠️ **DO NOT use the sentinel value `00000000-0000-0000-0000-000000000000`
> as your real `WORKSPACE_ID`.** The sentinel is a placeholder that the
> manual backfill script ships with so `sed` can substitute it for your real
> UUID. It is itself a syntactically valid UUID, so the database will
> happily accept it — but on any shared / forked / template DB every
> backfill will land on the same nil-UUID workspace and you will see
> cross-tenant data co-mingling. Always generate a fresh UUID below.

Generate one and save it. This is the value you will paste into both
the SQL script and the `WORKSPACE_ID` environment variable.

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

### Step 2 — substitute the sentinel and apply the script

`scripts/manual/000011_backfill_workspace_id.psql` binds the target UUID
through a psql `\set` variable baked into the file itself:

```sql
-- BACKFILL_WORKSPACE_ID is a sentinel — replace before applying.
\set BACKFILL_WORKSPACE_ID '''00000000-0000-0000-0000-000000000000'''
```

Because that `\set` line lives inside the script, a bare `psql -v
BACKFILL_WORKSPACE_ID=... -f scripts/manual/000011_backfill_workspace_id.psql`
invocation does **not** work — the script's own `\set` line runs after your
`-v` binding and silently overwrites it back to the nil sentinel. You have
to substitute the sentinel value itself. As before, do this on a `/tmp`
copy — never edit the file in the repo, since that leaves your UUID in the
worktree for a careless `git add` to leak:

```bash
cp scripts/manual/000011_backfill_workspace_id.psql /tmp/applied-000011.psql

sed -i \
  "s/00000000-0000-0000-0000-000000000000/$WORKSPACE_UUID/g" \
  /tmp/applied-000011.psql

# Apply against the target Postgres.
psql "$DATABASE_URL" -f /tmp/applied-000011.psql
```

> Note: BSD `sed` (macOS) requires `sed -i ''` instead of `sed -i`.

There is no `.down.sql` to keep around this time — see Rollback below if you
need to undo the backfill. Clean up once you're happy with Step 4
verification:

```bash
rm /tmp/applied-000011.psql
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

The legacy script ships no down migration. If you need to undo it (e.g. you
picked the wrong UUID), run the inverse `UPDATE` by hand, scoped to the UUID
you applied so other workspaces are untouched:

```bash
psql "$DATABASE_URL" -c "
  UPDATE goals             SET workspace_id = NULL WHERE workspace_id = '$WORKSPACE_UUID';
  UPDATE projects          SET workspace_id = NULL WHERE workspace_id = '$WORKSPACE_UUID';
  UPDATE tasks             SET workspace_id = NULL WHERE workspace_id = '$WORKSPACE_UUID';
  UPDATE activity_log      SET workspace_id = NULL WHERE workspace_id = '$WORKSPACE_UUID';
  UPDATE repos             SET workspace_id = NULL WHERE workspace_id = '$WORKSPACE_UUID';
  UPDATE decisions         SET workspace_id = NULL WHERE workspace_id = '$WORKSPACE_UUID';
  UPDATE session_handoffs  SET workspace_id = NULL WHERE workspace_id = '$WORKSPACE_UUID';
  UPDATE knowledge_items   SET workspace_id = NULL WHERE workspace_id = '$WORKSPACE_UUID';
  UPDATE concepts          SET workspace_id = NULL WHERE workspace_id = '$WORKSPACE_UUID';
  UPDATE review_schedule   SET workspace_id = NULL WHERE workspace_id = '$WORKSPACE_UUID';
  UPDATE pending_proposals SET workspace_id = NULL WHERE workspace_id = '$WORKSPACE_UUID';
"
```

Then unset `WORKSPACE_ID` (Railway: delete the variable; local: remove the
line from `.env`) and redeploy.

### SQLite self-hosters

The SQLite equivalent (the old `000011_backfill_workspace_id.up.sql` /
`.down.sql` pair that used to live under `migrations/sqlite/`) no longer
exists — it was deleted outright (not relocated to `scripts/manual/`) in the
same change that moved the Postgres script out of `migrations/`. There is no
legacy SQLite 000011 script to replay; use the SQLite procedure in
"WORKSPACE_ID Backfill (migration 000015)" below instead — it covers the
same 11 tables.

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
