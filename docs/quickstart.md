# Quickstart

Get wayneblacktea running locally in under 10 minutes.

## Prerequisites

- Go 1.26+ (`go version`)
- Node.js 20+ (for the web UI build step)
- A running PostgreSQL instance **or** set `STORAGE_BACKEND=sqlite` to skip Postgres entirely

## 1. Clone and configure

```bash
git clone https://github.com/Wayne997035/wayneblacktea.git
cd wayneblacktea
cp .env.example .env
```

Open `.env` and fill in at minimum:

| Variable | Required | Notes |
|----------|----------|-------|
| `API_KEY` | yes | Any random secret; used by the web UI and MCP client |
| `DATABASE_URL` | yes (Postgres) | libpq connection string, e.g. `postgres://user:pass@localhost:5432/wbt` |
| `WORKSPACE_ID` | yes | Any UUID, e.g. generate one with `uuidgen` |
| `STORAGE_BACKEND` | optional | `sqlite` for local dev without Postgres |

## 2. Build and start the server

```bash
npm run build             # builds web/dist (embedded in the Go binary)
go run ./cmd/server -env .env
```

The server listens on port 8080 by default. Open `http://localhost:8080` in your browser.

## 3. Run the seed command

The seed command populates the database with starter data (goals and workspace repos). It is safe to run multiple times — duplicates are skipped.

```bash
go run ./cmd/seed
```

### Demo mode

Pass `--demo` to also seed **realistic demo data** so you can explore every feature immediately:

```bash
go run ./cmd/seed --demo
```

Demo mode seeds:

| Entity | Count | Details |
|--------|-------|---------|
| Projects | 2 | "Personal OS Development" (engineering, priority 1) and "Learning Go" (learning, priority 2) |
| Tasks | 5 | Spread across both projects; one completed, two in-progress, two pending; importance 1–3 |
| Decisions | 3 | Architecture decisions covering the storage backend choice, dual-backend design, and the no-FK policy |
| Knowledge items | 2 | One `article` (Go scheduler internals) and one `til` (context.WithCancelCause) |
| Concepts | 1 | FSRS algorithm — seeded into spaced-repetition for immediate review scheduling |

All demo records are created idempotently: re-running `--demo` skips records that already exist.

#### What you can explore after running --demo

- **Dashboard** (`/` in the web UI) — shows active projects, next task, and weekly progress.
- **Tasks** (`GET /api/projects/personal-os-dev/tasks`) — see the mix of statuses and priorities.
- **Decisions** (`GET /api/decisions`) — read the three seeded architecture decision records.
- **Knowledge search** (`GET /api/knowledge/search?q=goroutine`) — test full-text search.
- **Learning reviews** (`GET /api/learning/reviews`) — the FSRS concept is ready for its first review.

## 4. Connect Claude Code (MCP)

```bash
claude mcp add --transport http wayneblacktea http://localhost:8080/mcp
```

Add `X-API-Key: <your API_KEY>` as a request header in the Claude Code MCP client config.

## Next steps

- Read `docs/architecture.md` for the system design.
- Read `docs/mcp-tools.md` for a full list of available MCP tools.
- Run `cd build && task check` to verify lint and tests pass before contributing.
