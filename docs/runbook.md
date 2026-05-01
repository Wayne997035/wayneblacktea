# wayneblacktea Operations Runbook

Production and self-host operational procedures. Day-to-day startup: see [`installation.md`](installation.md).

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

### Step 1 — generate a personal UUID

```bash
WORKSPACE_UUID=$(uuidgen | tr '[:upper:]' '[:lower:]')
echo "$WORKSPACE_UUID"
# Example: 6e6f7c40-2e45-4c98-9e2a-4f0a93e0e1aa

# Refuse to use the nil sentinel
[ "$WORKSPACE_UUID" = "00000000-0000-0000-0000-000000000000" ] && \
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
cp migrations/000011_backfill_workspace_id.up.sql   /tmp/applied-000011.up.sql
cp migrations/000011_backfill_workspace_id.down.sql /tmp/applied-000011.down.sql

# macOS: sed -i ''   Linux: sed -i
sed -i "s/00000000-0000-0000-0000-000000000000/$WORKSPACE_UUID/g" \
  /tmp/applied-000011.up.sql /tmp/applied-000011.down.sql

psql "$DATABASE_URL" -f /tmp/applied-000011.up.sql
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

Verify server picked it up: startup log shows `workspace scoping: enabled (uuid=...)`.

```bash
curl -H "Authorization: Bearer YOUR_API_KEY" https://your-host/api/projects \
  | jq '.[].workspace_id' | sort -u
# Should print only your $WORKSPACE_UUID
```

### Rollback — undo the backfill

```bash
psql "$DATABASE_URL" -f /tmp/applied-000011.down.sql
```

Sets `workspace_id = NULL` only on rows matching your UUID. Then unset `WORKSPACE_ID` and redeploy.

If `/tmp/applied-000011.down.sql` was deleted, re-run the `cp` + `sed` block from Step 3 with the same `WORKSPACE_UUID`.

### SQLite variant

```bash
cp migrations/sqlite/000011_backfill_workspace_id.up.sql   /tmp/applied-sqlite-000011.up.sql
cp migrations/sqlite/000011_backfill_workspace_id.down.sql /tmp/applied-sqlite-000011.down.sql
sed -i "s/00000000-0000-0000-0000-000000000000/$WORKSPACE_UUID/g" \
  /tmp/applied-sqlite-000011.up.sql /tmp/applied-sqlite-000011.down.sql
sqlite3 /path/to/data.db < /tmp/applied-sqlite-000011.up.sql
# Rollback: sqlite3 /path/to/data.db < /tmp/applied-sqlite-000011.down.sql
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
| `PGSSLROOTCERT` | No | Postgres CA cert (if not in system trust store) |
| `POSTGRES_INSECURE_TLS` | No | `true` for managed providers (Railway, Aiven) |
| `PORT` | No | Server port (default `8080`) |

Check Railway vars: `railway variables`

Never commit any of these values. Verify `.gitignore` covers `.env`, `.env.local`, `local.yaml` before every `git add`.
