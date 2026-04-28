# Self-hosting wayneblacktea

This document is the technical companion to the [README]. Everything
here is for someone running their own instance — not relevant to a
first-time reader of the project.

## Prerequisites

- Go 1.25+
- PostgreSQL 14+ with the `pgvector` extension
- Node 22+ (for the dashboard build step)
- Optional integrations: Discord bot token, Gemini API key, Groq API
  key, Notion integration secret. None are required to bring the
  server up; the missing pipeline simply degrades gracefully (no
  vector search, no Discord bot, etc.).

## First-time setup

```bash
cp .env.example .env             # fill in DATABASE_URL, API_KEY, …

# Apply migrations in order. 000011 is a backfill scaffold — open
# it, replace the sentinel UUID with one of your own (uuidgen | tr
# A-Z a-z), and run it manually if you want per-workspace scoping.
for f in migrations/0000*.up.sql; do psql "$DATABASE_URL" -f "$f"; done

cd build
task check                       # lint + tests + build (~30 s)
```

## Running

| Process | Build | Notes |
|---|---|---|
| HTTP API + Discord bot + scheduler | `task build-server` | The deployable. Reads `.env`. |
| MCP stdio server | `task build-mcp` | Wired into editor MCP config. Reads env from the calling process. |
| `wbt-doctor` | `task build-doctor` | One-shot binary used by a Stop hook to write `/tmp/wbt-health.json`. |
| Seeder | `go run ./cmd/seed` | First-time canonical goals + repos. |

```bash
./bin/wayneblacktea-server -env ../.env
```

## Environment variables

| Var | Required? | Purpose |
|---|---|---|
| `DATABASE_URL` | yes | Postgres DSN (`postgres://user:pass@host/db?sslmode=require`). |
| `API_KEY` | yes (server) | Bearer token for every `/api/*` route. |
| `STORAGE_BACKEND` | no | `postgres` (default). `sqlite` is in progress; setting it errors at startup until the SQLite stores ship. |
| `WORKSPACE_ID` | no | UUID scoping every read and write. Unset → legacy unscoped mode. |
| `USER_ID` | no | Identity tag used as `proposed_by` attribution. |
| `PORT` | no | HTTP port (default `8080`). |
| `ALLOWED_ORIGINS` | no | CORS list (default `*`). |
| `GEMINI_API_KEY` | no | Vector embeddings for knowledge dedup + similarity search. |
| `GROQ_API_KEY` | no | Discord bot LLM analyser for `/analyze`. |
| `DISCORD_BOT_TOKEN` | no | Discord bot session token. Bot disabled if unset. |
| `DISCORD_GUILD_ID` | no | Guild for instant slash command registration. Without it, registration is global (~1 h propagation). |
| `NOTION_INTEGRATION_SECRET` + `NOTION_DATABASE_ID` | no | `sync_to_notion` MCP tool target. |

## Workspace scoping

Setting `WORKSPACE_ID` to a UUID:

- Reads filter every domain query with the workspace predicate.
- Writes populate `workspace_id` on insert.
- Existing rows with `NULL` workspace_id become invisible. To bring
  them in, run `migrations/000011_backfill_workspace_id.up.sql`
  (replace the placeholder UUID first).

Leaving `WORKSPACE_ID` unset preserves single-tenant behaviour: no
filter, NULL on insert.

## Testing

```bash
cd build
task test                  # ≈30 s — pure unit (no DB).
task test-integration      # requires DATABASE_URL, runs //go:build integration tests.
task check                 # lint + test + build × 4 binaries — the gate.
```

## Deployment

The canonical production deployment is a single Railway service
built from `build/Dockerfile.server` (multi-stage: Node builds the
React dist, Go builds the static binary, alpine runtime). Healthcheck
hits `/health`. The database is managed Postgres with pgvector.

```bash
railway link --service <your service name>
railway up --ci -m "your message"
```

Tagged GitHub Releases produce cross-compiled binaries via
`.goreleaser.yml` (linux/darwin × amd64/arm64).

[README]: ../README.md
