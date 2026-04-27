# wayneblacktea

A personal-OS server for AI agents.

GTD task management, decision log, FSRS spaced-repetition learning,
knowledge base with vector search, agent-proposal gate, and an MCP
(Model Context Protocol) surface that lets Claude Code, a Discord bot,
and a React dashboard collaborate against a single shared brain — backed
by Postgres in production and (optionally) SQLite for friend-grade
self-hosting.

> ⚠️ Personal project, opened up to friends running self-hosted instances.
> The dashboard, Discord bot, and Aiven prod instance are tuned for one
> user (Wayne). If you fork to self-host, expect to flip a few env vars
> (`WORKSPACE_ID`, `STORAGE_BACKEND`) and own the Discord bot token.

---

## What it stores

| Bounded context | Tables | Surfaced via |
|---|---|---|
| **GTD** | `goals`, `projects`, `tasks` (incl. `importance` 1-3 + `context`), `activity_log` | MCP, REST, Web UI |
| **Decisions** | `decisions` (context / decision / rationale / alternatives) | MCP `log_decision`, REST `/api/decisions` |
| **Knowledge** | `knowledge_items` (article / til / bookmark / zettelkasten + pgvector embedding + FTS) | MCP `add_knowledge` / `search_knowledge`, Discord `/analyze` `/note` `/search` |
| **Learning** | `concepts`, `review_schedule` (FSRS stability/difficulty/due) | MCP `get_due_reviews` / `submit_review` / `create_concept`, Discord `/review` |
| **Sessions** | `session_handoffs` (cross-session continuity notes) | MCP `set_session_handoff` / `resolve_handoff` |
| **Proposals** | `pending_proposals` (agent-originated, awaiting user confirmation) | MCP `propose_goal` / `propose_project` / `list_pending_proposals` / `confirm_proposal` |
| **Workspace (repos)** | `repos` (tracked Git repos with status / known issues / next planned step) | MCP `list_active_repos` / `sync_repo` |

Every domain table carries a nullable `workspace_id`. Setting `WORKSPACE_ID`
in the environment scopes every read and write to that UUID; leaving it
unset keeps legacy single-user behaviour.

## Architecture

```
┌──────────────┐  ┌──────────────┐  ┌──────────────┐
│  Claude Code │  │  Discord Bot │  │  Web UI      │
│  (MCP stdio) │  │  (slash cmds │  │  (React 19)  │
│              │  │   + HTTP)    │  │              │
└──────┬───────┘  └──────┬───────┘  └──────┬───────┘
       │                 │                 │
       │   ┌─────────────┴─────────────────┘
       │   │
       ▼   ▼
┌──────────────────┐    ┌──────────────────┐
│  cmd/mcp         │    │  cmd/server      │
│  (33 MCP tools,  │    │  (Echo HTTP API, │
│   stdio)         │    │   23 endpoints)  │
└──────┬───────────┘    └──────┬───────────┘
       │                       │
       │  ┌────────────────────┘
       │  │
       │  │  ┌──────────────────────────────┐
       │  │  │  cmd/doctor (Stop hook)      │
       │  │  │  → /tmp/wbt-health.json     │
       │  │  └──────────────┬───────────────┘
       │  │                 │
       ▼  ▼                 ▼
┌──────────────────────────────────────┐
│  internal/<bounded-context>          │
│  Each: pg-backed Store +             │
│        StoreIface (interface lift)   │
│  + internal/proposal AutoPropose     │
│  + internal/watchdog (in-proc ring)  │
│  + internal/runtime WORKSPACE_ID env │
│  + internal/storage Backend factory  │
└──────────────────┬───────────────────┘
                   │
        ┌──────────┴──────────┐
        ▼                     ▼
   PostgreSQL            SQLite v1
   (+ pgvector,         (modernc.org,
    Aiven prod)          GTDStore only;
                         others TBD)
```

Each bounded context lives under `internal/<domain>/` with three files:

- `<domain>.go` — domain types and `Create*Params` / sentinel errors
- `store.go` — Postgres-backed `*Store` (sqlc + raw queries when needed)
- `iface.go` — `StoreIface` capturing the public surface; concrete
  `*Store` satisfies it via compile-time `var _ StoreIface = (*Store)(nil)`

The `iface.go` lift is what lets a future SQLite-backed store slot in
without touching HTTP handlers or MCP tool plumbing.

## Surface area

### MCP tools (33)

```
get_today_context        list_active_repos       sync_repo
list_projects            create_project          list_tasks
add_task                 complete_task           list_goals
create_goal              update_task             update_project_status
get_project              log_activity            delete_task
log_decision             list_decisions          add_knowledge
search_knowledge         list_knowledge          sync_to_notion
get_due_reviews          submit_review           create_concept
set_session_handoff      resolve_handoff         confirm_plan
propose_goal             propose_project         list_pending_proposals
confirm_proposal         system_health           initial_instructions
```

`add_task` accepts `importance` (1-3) and `context` parameters in
addition to title/priority. `add_knowledge` automatically files a
pending concept proposal for review-eligible item types
(article / til / zettelkasten) and returns the `concept_proposal_id`
so the caller can immediately confirm or ignore.

`system_health` is the watchdog readout — surfaces stuck in-progress
tasks, pending proposal queue depth, due review count, and recent
tool-call counters with "forgotten signals" (e.g. *"3 add_task without
a single complete_task"*).

### HTTP API (23 endpoints, all under `/api`, bearer-token via `X-API-Key`)

```
GET    /context/today
GET    /goals                       POST /goals
GET    /projects                    POST /projects
GET    /projects/:id                PATCH /projects/:id/status
GET    /projects/:id/tasks          POST /tasks
PATCH  /tasks/:id/status            PATCH /tasks/:id/complete
GET    /decisions                   POST /decisions
GET    /workspace/repos             POST /workspace/repos
GET    /session/handoff             POST /session/handoff
GET    /knowledge                   POST /knowledge
GET    /knowledge/search
GET    /learning/reviews            POST /learning/reviews/:id/submit
POST   /learning/concepts
```

`POST /api/knowledge` triggers the same auto-propose-concept flow as
the MCP tool, so the Discord bot's knowledge ingest also feeds the
spaced-repetition queue.

### Discord slash commands (5)

| Command | What it does | Backed by |
|---|---|---|
| `/analyze input:<url\|text>` | Fetch + Groq-analyze + (if valuable) save to knowledge base | POST /api/knowledge |
| `/note text:<…>` | Save a quick TIL directly | POST /api/knowledge |
| `/search query:<…>` | FTS + vector search across knowledge | GET /api/knowledge/search |
| `/recent` | Last 5 saved items | GET /api/knowledge |
| `/review title:<…> content:<…> tags:<a,b>` | Drop a concept card straight into the FSRS queue (skips proposal gate) | POST /api/learning/concepts |

A `!command` text alias exists for every slash command for quick
mobile use (`!review title :: content :: a,b`).

## Binaries (`cmd/*`)

| Binary | Built via | Role |
|---|---|---|
| `wayneblacktea-server` | `task build-server` | Echo HTTP API + Discord bot + scheduler (Railway-deployed) |
| `wayneblacktea-mcp` | `task build-mcp` | MCP stdio server for Claude Code (run locally) |
| `wbt-doctor` | `task build-doctor` | One-shot health snapshot for Stop hooks → `/tmp/wbt-health.json` |
| `cmd/seed` | manual `go run` | Seed canonical goals + repos on a fresh DB |
| `cmd/discordbot` | embedded in server | Standalone bot binary kept for local dev |

## Storage backends

`STORAGE_BACKEND` env selects the engine:

| Value | Status |
|---|---|
| `postgres` (default) | ✅ Production. pgxpool + pgvector. The only path that runs the live deployment. |
| `sqlite` | ⚠️ v1 partial. `internal/storage/sqlite/GTDStore` is feature-complete with 10 unit tests; the other six domain stores (decision/session/workspace/knowledge/learning/proposal) are not yet ported. Setting `STORAGE_BACKEND=sqlite` errors at startup until they land. Pure-Go `modernc.org/sqlite` driver chosen so cross-compilation stays CGo-free. |

## "Forgotten signals" — the anti-amnesia layer

LLMs forget to call MCP tools. Even when `CLAUDE.md` says
`MANDATORY: complete_task after work is done`, the assistant skips it
under load. wayneblacktea ships a two-part counter-measure:

1. **In-process watchdog** (`internal/watchdog`): an `mcp-go`
   `ToolHandlerMiddleware` records every tool invocation in a 200-entry
   ring buffer. The `system_health` MCP tool aggregates this into
   *forgotten signals* — short human-readable strings like
   *"5+ pending proposals queued — triage backlog"* or *"decisions
   logged but no get_today_context this session"*.
2. **Out-of-process doctor** (`cmd/doctor`): runs as a Claude Code
   `Stop` hook, queries the same DB directly (the MCP process is gone
   by Stop time), and writes `/tmp/wbt-health.json`. The next
   `SessionStart` hook reads it and pushes the signals into Claude's
   `additionalContext`, so the next session sees the leftover open
   loops before the user even types.

Hook wiring (`_project/.claude/settings.json` if you self-host) is
documented inline in `_project/.claude/hooks/wbt-doctor.sh` and
`session-start.sh`.

## Running locally

### Prerequisites

- Go 1.25.0+
- PostgreSQL 14+ with `pgvector` and `uuid-ossp` extensions
  (Aiven, Railway, supabase, plain docker — anything that supports both)
- Node 22+ + npm (for the React dashboard)
- (optional) `sqlc` 1.31+ if you regenerate query code
- (optional) Discord bot token + Gemini API key + Groq API key for
  the full pipeline

### First time

```bash
cp .env.example .env             # fill in DATABASE_URL, API_KEY, …

# Apply migrations in order. Skip 000011 — it is a backfill scaffold,
# not auto-runnable. Open it, replace the sentinel UUID with one of
# your own (uuidgen | tr A-Z a-z), then run it manually if you want
# to enforce per-workspace scoping.
for f in migrations/0000*.up.sql; do psql "$DATABASE_URL" -f "$f"; done

cd build
task check                       # lint + tests + binaries (~30s)
task build-server                # produces bin/wayneblacktea-server
task build-mcp                   # produces bin/wayneblacktea-mcp
task build-doctor                # produces bin/wbt-doctor
```

### Run

```bash
./bin/wayneblacktea-server -env ../.env       # HTTP API + Discord bot
# or, for stdio MCP only (e.g. wired into Claude Code):
./bin/wayneblacktea-mcp                        # reads DATABASE_URL from env
```

The included `.mcp.json` snippet wires the local MCP binary into
Claude Code; the included `railway.toml` deploys `cmd/server` to
Railway via `Dockerfile.server`.

## Environment variables

| Var | Required? | Purpose |
|---|---|---|
| `DATABASE_URL` | yes | Postgres DSN. `sslmode=require` works against Aiven, Railway, Neon, supabase. |
| `API_KEY` | yes (server) | Bearer token for every `/api/*` route. |
| `STORAGE_BACKEND` | no | `postgres` (default) or `sqlite` (v1 partial — see above). |
| `WORKSPACE_ID` | no | UUID scoping every read+write. Unset → legacy unscoped. |
| `USER_ID` | no | Identity tag for `proposed_by` attribution. Schema doesn't have a `user_id` column yet (deferred). |
| `PORT` | no | HTTP port (default `8080`). |
| `ALLOWED_ORIGINS` | no | CORS list (default `*`). |
| `GEMINI_API_KEY` | no | Vector embeddings for knowledge dedup + similarity search. Falls back to URL-only dedup if unset. |
| `GROQ_API_KEY` | no | Discord bot LLM analyzer for `/analyze`. |
| `DISCORD_BOT_TOKEN` | no | Discord bot session token. Bot disabled if unset. |
| `DISCORD_GUILD_ID` | no | Guild for slash command registration (instant). Without it, registration is global (~1h propagation). |
| `NOTION_INTEGRATION_SECRET` + `NOTION_DATABASE_ID` | no | `sync_to_notion` MCP tool. |

## Workspace scoping (Phase B2)

Setting `WORKSPACE_ID` to a UUID:

- Reads filter every domain query with `(?ws::uuid IS NULL OR workspace_id = ?ws)`.
- Writes populate `workspace_id` on insert.
- Existing rows with `NULL` workspace_id become invisible. To bring
  them in, run `migrations/000011_backfill_workspace_id.up.sql`
  (replace the placeholder UUID first).

Leaving `WORKSPACE_ID` unset preserves legacy behaviour: no filter,
NULL on insert. Single-user instances need not set it.

## Deployment

The canonical production deployment is a Railway project:

- Service: `wayneblacktea` (one container)
- Image: built from `build/Dockerfile.server` — multi-stage, Node
  builds the React dist, Go builds the static binary (`CGO_ENABLED=0`),
  alpine runtime
- Healthcheck: `/health`
- Database: Aiven managed Postgres (with pgvector)
- Live at <https://wayneblacktea-production.up.railway.app>

Deploy via Railway CLI:

```bash
railway link --service wayneblacktea
railway up --ci -m "your message here"
```

Tagged GitHub Releases are produced by `.goreleaser.yml` (cross-compile
linux/darwin × amd64/arm64; archives bundle migrations and SQL
queries alongside the binaries).

## Testing

```bash
cd build
task test                  # ≈30s  — pure unit (no DB)
task test-integration      # requires DATABASE_URL; runs all `//go:build integration` tests
task check                 # lint + test + build × 4 binaries — the gate
```

The integration tag covers every store-level test (Postgres) plus the
SQLite v1 GTDStore tests (which always pass — they use in-memory
SQLite, not the real DB).

## Repo layout

```
cmd/
  server/        — HTTP API + Discord bot + scheduler
  mcp/           — stdio MCP server
  doctor/        — wbt-doctor health snapshot binary (Stop hook)
  discordbot/    — standalone bot for local dev
  seed/          — first-time DB seeding
internal/
  gtd, decision, session, workspace, knowledge, learning, proposal
                 — bounded contexts (each: types + Store + StoreIface)
  db             — sqlc generated code (DO NOT EDIT)
  handler        — Echo HTTP handlers (interfaces in interfaces.go)
  middleware     — API-key, CORS
  mcp            — mcp-go server + tool registrations + health watchdog
  watchdog       — in-process tool-call ring buffer
  runtime        — WORKSPACE_ID / USER_ID env reading
  storage        — Backend enum + sqlite/ subpackage (v1 partial)
  search         — Gemini embedding client + RRF
  scheduler      — gocron jobs (FSRS reviews, daily digest, …)
  notion, discord, discordbot
                 — third-party integrations
migrations/      — golang-migrate compatible (.up.sql / .down.sql pairs)
sql/queries/     — sqlc input
build/           — Dockerfile.server, fly.toml, Taskfile.yml
web/             — React 19 + Vite + Tailwind v4 + Zustand + TanStack Query
```

## Roadmap

What this repo *currently does* is captured in [CHANGELOG.md].
What's *next*:

- **SQLite v2** — port the remaining six domain stores so
  `STORAGE_BACKEND=sqlite` becomes a real friend-install path.
- **SQLite v3** — FTS5 + sqlite-vec for proper knowledge search.
- **Compile-time capability gate** for agents (an `Authorized`-token
  pattern) — currently a backlog task; the watchdog covers the
  detection side, but mutation methods are not yet capability-gated.
- **CI/CD** — `.github/workflows/` for `task check` on PR and
  goreleaser on tag.

## Inspirations & acknowledgments

The "personal OS / agents share a semantic runtime" framing is a
small-but-active design direction in 2025-2026 indie tooling, and
several open repos have been valuable references — most notably
[koopa](https://github.com/Koopa0/koopa) (capability-gate pattern,
proposal-gate concept). Code in this repository is original; database
schema, MCP tool design, store layout, frontend stack, and licensing
are all distinct.

Standard-library and ecosystem dependencies driving most of the
heavy lifting:

- [`mark3labs/mcp-go`](https://github.com/mark3labs/mcp-go) — MCP server
- [`jackc/pgx/v5`](https://github.com/jackc/pgx) — Postgres driver
- [`pgvector/pgvector-go`](https://github.com/pgvector/pgvector-go) — vector ops
- [`labstack/echo/v4`](https://echo.labstack.com) — HTTP
- [`bwmarrin/discordgo`](https://github.com/bwmarrin/discordgo) — Discord
- [`go-co-op/gocron/v2`](https://github.com/go-co-op/gocron) — scheduler
- [`modernc.org/sqlite`](https://gitlab.com/cznic/sqlite) — pure-Go SQLite
- React 19 / Vite 7 / Tailwind v4 / Zustand 5 / TanStack Query v5

## Contributing

See [CONTRIBUTING.md]. TL;DR: branch off `master`, write a 6-element
dispatch prompt for yourself, run `task check` until green, one
logical change per commit (`feat:` / `fix:` / `chore:` prefix, no
scope suffix), open a PR.

## License

[MIT](./LICENSE).

[CHANGELOG.md]: ./CHANGELOG.md
[CONTRIBUTING.md]: ./CONTRIBUTING.md
