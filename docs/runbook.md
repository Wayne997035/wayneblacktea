# wayneblacktea Operations Runbook

Production and self-host operational procedures. Day-to-day startup: see [`install.md`](install.md).

---

## 1. Migration up

### Postgres

```bash
export DATABASE_URL="postgres://USER:PASS@HOST:PORT/DBNAME?sslmode=require"

# Apply all pending migrations
for f in migrations/0000*.up.sql; do
  psql "$DATABASE_URL" -f "$f"
done

# Or with golang-migrate:
migrate -path migrations/ -database "$DATABASE_URL" up

# Or with Task:
cd build && task migrate-up   # requires DATABASE_URL in build/.env.local
```

### SQLite

SQLite schema applies idempotently at boot. For individual patches:

```bash
sqlite3 /path/to/data.db < migrations/sqlite/000014_project_arch.up.sql
```

---

## 2. Migration rollback

### Postgres

```bash
migrate -path migrations/ -database "$DATABASE_URL" down 1
# Or: cd build && task migrate-down
```

Verify: `psql "$DATABASE_URL" -c "SELECT version FROM schema_migrations ORDER BY version DESC LIMIT 3;"`

If data was lost, restore from a pre-migration backup (Section 3), then re-apply subsequent up migrations.

### SQLite

```bash
sqlite3 /path/to/data.db < migrations/sqlite/000014_project_arch.down.sql
```

---

## 3. DB backup

### Postgres

```bash
pg_dump "$DATABASE_URL" --format=custom \
  --file="/tmp/wayneblacktea-$(date +%Y%m%d-%H%M%S).pgdump"

# Restore:
pg_restore --dbname="$DATABASE_URL" --verbose /tmp/wayneblacktea-YYYYMMDD-HHMMSS.pgdump
```

Railway: use the Postgres service → Backups tab in the dashboard, or copy `DATABASE_URL` and run `pg_dump` locally.

### SQLite

```bash
# Online backup (no stop required):
sqlite3 /path/to/data.db ".backup /backup/wayneblacktea-$(date +%Y%m%d-%H%M%S).db"
```

---

## 4. WORKSPACE_ID backfill SOP

Run once when enabling workspace scoping on an existing database with NULL `workspace_id` rows.

Affected tables (11): `goals`, `projects`, `tasks`, `activity_log`, `repos`, `decisions`, `session_handoffs`, `knowledge_items`, `concepts`, `review_schedule`, `pending_proposals`.

> **Note on legacy 000011 backfill** — the original `migrations/000011_backfill_workspace_id.up.sql`
> used psql metacommands (`\set`) that golang-migrate cannot parse. It has been
> moved out of the embedded `migrations/` tree to
> `scripts/manual/000011_backfill_workspace_id.psql` so fresh-DB spinup no
> longer fails. A no-op marker (`migrations/000036_legacy_011_marker.up.sql`)
> keeps the historical schema_migrations row consistent. The canonical SOP
> below uses 000015 (no metacommands, plain SQL) and is the recommended path
> for any new install.

### Step 1 — generate a personal UUID

```bash
WORKSPACE_UUID=$(uuidgen | tr '[:upper:]' '[:lower:]')
echo "$WORKSPACE_UUID"
# Example: 6e6f7c40-2e45-4c98-9e2a-4f0a93e0e1aa

# Refuse to use the nil sentinel
[ "$WORKSPACE_UUID" = "00000000-0000-0000-0000-000000000001" ] && \
  { echo "ERROR: nil sentinel not allowed" >&2; exit 1; }
```

### Step 2 — dry-run: count NULL rows

```bash
psql "$DATABASE_URL" -c \
  "SELECT 'goals' AS t, COUNT(*) FROM goals WHERE workspace_id IS NULL
   UNION ALL SELECT 'projects', COUNT(*) FROM projects WHERE workspace_id IS NULL
   UNION ALL SELECT 'tasks', COUNT(*) FROM tasks WHERE workspace_id IS NULL;"
```

Zero counts = no rows to backfill; skip to Step 3.

### Step 3 — copy, substitute, and apply

Copy to `/tmp` — never edit the repo files in place (would leave your UUID in the worktree and break the sentinel safety guarantee on re-runs).

```bash
cp migrations/000015_workspace_id_backfill.up.sql   /tmp/applied-000015.up.sql
cp migrations/000015_workspace_id_backfill.down.sql /tmp/applied-000015.down.sql

# macOS: sed -i ''   Linux: sed -i
sed -i "s/00000000-0000-0000-0000-000000000001/$WORKSPACE_UUID/g" \
  /tmp/applied-000015.up.sql /tmp/applied-000015.down.sql

psql "$DATABASE_URL" -f /tmp/applied-000015.up.sql
```

### Step 4 — verify

```bash
psql "$DATABASE_URL" -c \
  "SELECT 'goals' AS t, COUNT(*) FROM goals WHERE workspace_id = '$WORKSPACE_UUID'
   UNION ALL SELECT 'projects', COUNT(*) FROM projects WHERE workspace_id = '$WORKSPACE_UUID'
   UNION ALL SELECT 'tasks', COUNT(*) FROM tasks WHERE workspace_id = '$WORKSPACE_UUID';"
```

Counts should match Step 2.

### Step 5 — set WORKSPACE_ID in the runtime environment

**Railway:** Variables tab → New Variable: `WORKSPACE_ID = <YOUR_WORKSPACE_UUID>`. Save; Railway redeploys.

**Local:** `echo "WORKSPACE_ID=$WORKSPACE_UUID" >> .env`

Verify server picked it up via the API (no startup log line is emitted today —
verification is API-side):

```bash
curl -H "Authorization: Bearer YOUR_API_KEY" https://your-host/api/projects \
  | jq '.[].workspace_id' | sort -u
# Should print only your $WORKSPACE_UUID — anything else means the env var
# was not loaded or got merged with rows that were never backfilled.
```

If the response is empty, hit `/api/dashboard/stats?period=7` and confirm
`workspace` field on the snapshot equals your UUID. If neither matches,
restart the server — `runtime.WorkspaceIDFromEnv()` runs at boot only.

### Rollback — undo the backfill

```bash
psql "$DATABASE_URL" -f /tmp/applied-000015.down.sql
```

Sets `workspace_id = NULL` only on rows matching your UUID. Then unset `WORKSPACE_ID` and redeploy.

If `/tmp/applied-000015.down.sql` was deleted, re-run the `cp` + `sed` block from Step 3 with the same `WORKSPACE_UUID`.

### SQLite variant

```bash
cp migrations/sqlite/000015_workspace_id_backfill.up.sql   /tmp/applied-sqlite-000015.up.sql
cp migrations/sqlite/000015_workspace_id_backfill.down.sql /tmp/applied-sqlite-000015.down.sql
sed -i "s/00000000-0000-0000-0000-000000000001/$WORKSPACE_UUID/g" \
  /tmp/applied-sqlite-000015.up.sql /tmp/applied-sqlite-000015.down.sql
sqlite3 /path/to/data.db < /tmp/applied-sqlite-000015.up.sql
# Rollback: sqlite3 /path/to/data.db < /tmp/applied-sqlite-000015.down.sql
```

---

## 5. Session hook config

Add to `~/.claude/settings.json` (or `.claude/settings.json` in the project):

```json
{
  "hooks": {
    "Stop": [{
      "matcher": "",
      "hooks": [{"type": "command", "command": "/abs/path/to/scripts/wbt-stop-hook.sh"}]
    }]
  }
}
```

The hook reads `API_KEY` from `.env` / `.env.local` and `WBT_API_URL` from the environment (defaults to the production Railway URL). For local dev:

```bash
export WBT_API_URL="http://localhost:8080"
```

The hook exits silently on any failure — it will not block Claude Code from closing.

---

## 6. Stop hook crash recovery

If the Stop hook could not reach the server, manually create the handoff:

```bash
curl -s -X POST https://your-host/api/session/handoff \
  -H "Authorization: Bearer YOUR_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"intent":"Describe what to continue","repo_name":"wayneblacktea","context_summary":"..."}'
```

Or via MCP in a new session: call `set_session_handoff`.

The next session's `get_today_context` will surface the pending handoff automatically.

---

## 7. Production environment variable checklist

| Variable | Required | Description |
|----------|----------|-------------|
| `API_KEY` | Yes | Bearer token for all `/api/*` routes. Generate: `openssl rand -hex 32` |
| `DATABASE_URL` | Yes (Postgres) | Postgres DSN |
| `ALLOWED_ORIGINS` | Yes | Explicit CORS origins — never `*` |
| `WORKSPACE_ID` | Recommended | UUID scoping data to your personal workspace |
| `CLAUDE_API_KEY` | No | AI summarisation and activity classification |
| `GEMINI_API_KEY` | No | Vector embeddings for semantic search |
| `GROQ_API_KEY` | No | Discord bot `/analyze` command |
| `DISCORD_BOT_TOKEN` | No | Enables Discord integration |
| `DISCORD_GUILD_ID` | No | Restricts Discord bot to one server |
| `NOTION_INTEGRATION_SECRET` | No | `sync_to_notion` MCP tool |
| `NOTION_DATABASE_ID` | No | Target Notion database |
| `PGSSLROOTCERT` | No | Path to Postgres CA cert bundle. Required when `APP_ENV=production` and your provider uses a custom CA (Aiven). Railway standard Postgres uses system CAs — leave unset. |
| `PORT` | No | Server port (default `8080`) |

Check Railway vars: `railway variables`

Never commit any of these values. Verify `.gitignore` covers `.env`, `.env.local`, `local.yaml` before every `git add`.

---

## 8. Common troubleshooting

### Server fails to start: `ErrMissingPGSSLROOTCERT`

```
storage: PGSSLROOTCERT required in production
```

Set `APP_ENV=production` only if you have `PGSSLROOTCERT` pointing to the CA cert file for your Postgres provider. Railway standard Postgres uses public CAs — either unset `APP_ENV` or set it to any value other than `production`.

For Aiven Postgres: download the CA bundle from the Aiven console → Connection Information → CA Certificate, then:

```bash
railway variables --set PGSSLROOTCERT=/app/ca.pem
```
And include the cert file in your Docker image or bind-mount it.

### MCP tool call returns 401

The HTTP MCP transport at `/mcp` requires `X-API-Key: <API_KEY>` on every request. Verify the key matches `API_KEY` env var.

```bash
curl -H "X-API-Key: $API_KEY" -X POST https://your-host/mcp
```

### Vision items not showing after `add_vision_item`

Check migration 000029 ran on your database:

```bash
psql "$DATABASE_URL" -c "\d vision_items"  # should print the table schema
```

If missing, apply it:

```bash
psql "$DATABASE_URL" -f migrations/000029_vision_items.up.sql
```

SQLite: schema is applied automatically at boot via `internal/storage/sqlite/schema.sql`.

### Knowledge navigate returns empty

Migration 000027 adds `parent_id`, `heading_path`, `heading_level` columns. Existing rows default to `NULL` — they behave as root items. Only new items added after the migration fan out to child nodes.

### `wbt serve` exits: `wayneblacktea-server not found in PATH`

Build the server binary and put it in your PATH:

```bash
go build -o "$(go env GOPATH)/bin/wayneblacktea-server" ./cmd/server
```

### Migrations: schema_migrations table vs idempotent up scripts

golang-migrate records applied migrations in `schema_migrations`. Re-running an already-applied up script directly with `psql -f` is safe (uses `IF NOT EXISTS` / `IF EXISTS`), but golang-migrate's `migrate up` will skip already-recorded versions. Use `migrate version` to check current version.

---

## 9. Observability TTL retention policies

Backed by `backend-security-design.md §1.3` — every observability table MUST
have a working retention policy in code, not just in design docs. The wayneblacktea
server runs the cleanup automatically via the embedded scheduler when a
Postgres pool is wired in; SQLite installs are dev-local single-tenant and do
not need TTL. For operators who want to force a cleanup on demand (e.g. before
a snapshot dump), a Taskfile target wraps each policy.

### `discipline_events` — 30-day TTL

Records every MCP tool invocation for drift-detection. Daily prune at 23:00
Asia/Taipei via the scheduler.

```bash
# Force on-demand prune (alternative to scheduler trigger):
cd build && task discipline-prune
```

### `pending_proposals` — 90 / 180-day dual TTL

The auto-decision-proposer middleware can fill the queue with up to one row
per mutating tool call. Two retention rules, both enforced by a single nightly
DELETE at 03:00 Asia/Taipei (offset from the 23:00 cluster to spread DB load):

| Status                      | Retention | Rationale                                                    |
|-----------------------------|-----------|--------------------------------------------------------------|
| `accepted` / `rejected`     | 90 days   | User has acted; keep ~1 quarter for audit, then drop         |
| `pending` & `type=decision` | 180 days  | Auto-proposer noise — stale pending decisions are obsolete   |
| `pending` & other types     | NEVER     | Goal/project/concept etc. = unresolved user intent; keep all |

```bash
# Force on-demand prune (alternative to scheduler trigger):
cd build && task pending-proposals-prune
```

The `WBT_DISABLE_AUTO_DECISIONS` env var (truthy values: `1`/`true`/`yes`/`on`)
disables the proposer if it produces too much queue noise; the prune still
runs and cleans up pre-existing rows.
