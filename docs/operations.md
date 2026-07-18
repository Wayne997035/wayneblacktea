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

**Recommended path for new installs:** use the WORKSPACE_ID backfill SOP in
[`runbook.md` §4](runbook.md#4-workspace_id-backfill-sop) instead — it covers
migration 000015 (plain SQL, no psql metacommands, proper `.down.sql`) for
both Postgres and SQLite. Only follow the procedure below if you specifically
need to replay the legacy 000011 script — e.g. reconciling a database that
already has schema_migrations row 000011 applied, or investigating
pre-000015 history.

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

Verify the server picked it up — no startup log line is emitted for this,
verification is API-side — by querying any list endpoint:

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

This is the routine, day-to-day WORKSPACE_ID backfill SOP (dry-run, generate
UUID, apply, verify, rollback — both Postgres and SQLite). It has moved to
[`runbook.md` §4 — WORKSPACE_ID backfill SOP](runbook.md#4-workspace_id-backfill-sop)
so there is one canonical copy instead of two drifting ones. Run this if you
are enabling workspace scoping on a production DB that was not covered by
`000011` (e.g. Railway instances where `000011` was skipped because it was
marked NOT AUTO-RUN).
