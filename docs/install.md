# Installation guide

This document covers all three installation modes and security notes.
For the self-hosting reference (migrations, workspace scoping, Railway deployment) see [`installation.md`](./installation.md).

## Prerequisites

| Tool | Minimum version | Notes |
|------|----------------|-------|
| Go | 1.26 | Required to build all binaries |
| Node.js | 22 | Required to build the React dashboard |
| Task | latest stable | `go install github.com/go-task/task/v3/cmd/task@latest` |
| PostgreSQL + pgvector | 14+ | Postgres mode only |

Optional integrations (none are required to start the server):

| Integration | Variable | Degrades to |
|-------------|----------|-------------|
| Claude AI | `CLAUDE_API_KEY` | No AI summarisation or activity classification |
| Gemini | `GEMINI_API_KEY` | No vector embeddings; knowledge dedup falls back to URL-only |
| Groq | `GROQ_API_KEY` | Discord bot `/analyze` command unavailable |
| Discord | `DISCORD_BOT_TOKEN` | Bot disabled entirely |
| Notion | `NOTION_INTEGRATION_SECRET` + `NOTION_DATABASE_ID` | `sync_to_notion` tool errors gracefully |

## Mode 1: SQLite local (zero infra)

Best for: solo development, trying wayneblacktea without setting up Postgres.

```bash
git clone https://github.com/Wayne997035/wayneblacktea.git
cd wayneblacktea

# Build the wbt installer CLI
go build -o bin/wbt ./cmd/wbt

# Run the interactive wizard -- choose [1] SQLite when prompted
./bin/wbt init
# Wizard writes:
#   .env       (STORAGE_BACKEND=sqlite, SQLITE_PATH=~/.wayneblacktea/data.db, API_KEY=<random>)
#   .mcp.json  (points Claude Code at wayneblacktea-mcp)

# Build and start the server
cd build && task build-server build-mcp && cd ..
./bin/wbt serve
# Server runs on http://localhost:8080
```

What `wbt init` asks:

1. `CLAUDE_API_KEY` -- your Anthropic API key (required for AI features)
2. Database: `[1] SQLite` or `[2] Postgres`
3. For SQLite: local file path (default `~/.wayneblacktea/data.db`)
4. Server port (default `8080`)
5. `API_KEY` -- auto-generates a random key if you press Enter

## Mode 2: PostgreSQL

Best for: full feature set including vector semantic search.

Your Postgres instance must have the `pgvector` extension:

```sql
CREATE EXTENSION IF NOT EXISTS vector;
```

Managed providers (Railway, Aiven, Supabase) have pgvector available -- enable it from their dashboard or run the SQL above.

```bash
git clone https://github.com/Wayne997035/wayneblacktea.git
cd wayneblacktea

go build -o bin/wbt ./cmd/wbt

# Run wizard -- choose [2] Postgres when prompted
./bin/wbt init
# Wizard prompts for: DATABASE_URL (postgres://USER:PASS@HOST:PORT/DB?sslmode=require)

# Apply database migrations
for f in migrations/0000*.up.sql; do psql "$DATABASE_URL" -f "$f"; done

cd build && task build-server build-mcp && cd ..
./bin/wbt serve
```

For manual setup, copy the example environment file:

```bash
cp .env.example .env
```

Postgres-mode required variables:

| Variable | Example value | Purpose |
|----------|--------------|---------|
| `DATABASE_URL` | `postgres://USER:PASS@HOST:PORT/DB?sslmode=require` | Postgres DSN |
| `API_KEY` | (generate: `openssl rand -hex 32`) | Bearer token for all `/api/*` routes |
| `ALLOWED_ORIGINS` | `http://localhost:5173` | CORS origins for the dashboard |

Set `POSTGRES_INSECURE_TLS=true` when using managed Postgres providers that use a custom CA not in the system trust store (Railway, Aiven).

## Mode 3: Docker

Best for: reproducible builds, self-hosting on a VPS or Railway.

```bash
git clone https://github.com/Wayne997035/wayneblacktea.git
cd wayneblacktea

cp .env.example .env
# Edit .env: set API_KEY, DATABASE_URL, ALLOWED_ORIGINS at minimum

API_KEY=$(grep ^API_KEY .env | cut -d= -f2)
docker build \
  --build-arg VITE_API_KEY="${API_KEY}" \
  -f build/Dockerfile \
  -t wayneblacktea .

docker run \
  --rm \
  -p 8080:8080 \
  --env-file .env \
  wayneblacktea
```

The Dockerfile is a three-stage build:

1. **Node 22-alpine** -- builds the React dashboard (`npm ci && npm run build`)
2. **golang:1.26-alpine** -- builds the Go server binary with the dashboard embedded
3. **alpine:3.21** -- minimal runtime (non-root user, `ca-certificates` only)

Healthcheck: `GET /health` returns `200 OK` when the server is ready.

To pass the Postgres DSN directly:

```bash
docker run \
  --rm \
  -p 8080:8080 \
  -e API_KEY=your-api-key \
  -e DATABASE_URL=postgres://USER:PASS@HOST:PORT/DB?sslmode=require \
  -e ALLOWED_ORIGINS=https://your-domain.example \
  -e POSTGRES_INSECURE_TLS=true \
  wayneblacktea
```

## Connecting Claude Code (MCP)

`wbt init` writes `.mcp.json` in the project root. Open Claude Code from that directory and it picks up the MCP server automatically.

For manual setup, `.mcp.json` has this shape:

```json
{
  "mcpServers": {
    "wayneblacktea": {
      "command": "/path/to/bin/wayneblacktea-mcp",
      "env": {
        "API_KEY": "your-api-key",
        "SERVER_URL": "http://localhost:8080"
      }
    }
  }
}
```

After loading, ask Claude Code to call `get_today_context` to verify the connection.

## Security notes

- **Never commit `.env`** -- it is listed in `.gitignore`. Verify before every `git add`.
- **`CLAUDE_API_KEY` and other API keys** must be set as environment variables in production. Never hardcode them.
- **`API_KEY`** gates every `/api/*` route. Use a random string of at least 32 characters (`openssl rand -hex 32`).
- **`ALLOWED_ORIGINS`** must be explicit origins, not `*`. Wildcard will panic at startup.
- **`PGSSLROOTCERT`** -- if your Postgres CA is not in the system trust store and you cannot use `POSTGRES_INSECURE_TLS`, point this variable at your CA certificate file.
- **`VITE_API_KEY`** is baked into the frontend bundle at build time. It matches `API_KEY` -- regenerate both together if compromised.

See [`docs/ci-secrets.md`](./ci-secrets.md) for CI/CD secret management.

## Upgrading

```bash
git pull
for f in migrations/0000*.up.sql; do psql "$DATABASE_URL" -f "$f"; done
cd build && task build-server build-mcp && cd ..
```

Migrations are idempotent -- running already-applied migrations is safe.
