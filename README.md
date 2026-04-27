# wayneblacktea

A personal-OS server for AI agents: GTD task management, decision log,
spaced-repetition learning, knowledge base with vector search, and an MCP
(Model Context Protocol) surface that lets Claude Code, Discord bots, and
other agents collaborate against a single shared brain.

## What it does

| Domain | What it stores | Surfaced through |
|---|---|---|
| **GTD** | Goals → Projects → Tasks (importance + context + priority) | MCP tools, REST API |
| **Decisions** | Architectural / design decisions with context, rationale, alternatives | MCP `log_decision`, REST `/api/decisions` |
| **Knowledge** | Articles, TILs, bookmarks, zettelkasten — full-text + pgvector similarity | MCP `add_knowledge` / `search_knowledge`, Discord ingest |
| **Learning** | Concept cards on FSRS spaced-repetition schedule | MCP `get_due_reviews` / `submit_review` |
| **Sessions** | Cross-session handoff notes (resume "tomorrow") | MCP `set_session_handoff` |
| **Proposals** | Agent-originated suggestions awaiting user confirmation | MCP `propose_goal` / `propose_project` / `confirm_proposal` |
| **Workspace (repos)** | Tracked Git repos with status / issues / next planned step | MCP `list_active_repos` / `sync_repo` |

## Architecture

```
┌──────────────┐  ┌──────────────┐  ┌──────────────┐
│  Claude Code │  │  Discord Bot │  │  Web UI      │
│  (MCP stdio) │  │  (HTTP API)  │  │  (HTTP API)  │
└──────┬───────┘  └──────┬───────┘  └──────┬───────┘
       │                 │                 │
       │   ┌─────────────┴─────────────────┘
       │   │
       ▼   ▼
┌──────────────────┐    ┌──────────────────┐
│  cmd/mcp         │    │  cmd/server      │
│  (MCP stdio)     │    │  (Echo HTTP)     │
└──────┬───────────┘    └──────┬───────────┘
       │                       │
       └───────────┬───────────┘
                   ▼
       ┌─────────────────────────┐
       │  internal/{gtd,         │
       │    decision, session,   │
       │    knowledge, learning, │
       │    workspace, proposal} │
       │  Domain Stores (iface.go│
       │  + concrete pg Store)   │
       └───────────┬─────────────┘
                   ▼
       ┌─────────────────────────┐
       │  internal/db (sqlc gen) │
       │  + raw pg queries       │
       └───────────┬─────────────┘
                   ▼
            PostgreSQL
            (+ pgvector ext)
```

Each domain is a bounded context with its own Store + sqlc query file. The
HTTP and MCP layers consume **interfaces** (`StoreIface`); a future SQLite
backend can be slotted in without touching either layer (see `internal/storage`
package).

### Phase summary

- ✅ **Phase A**: schema (workspace_id on every table, tasks importance/context, pending_proposals)
- ✅ **Phase B1**: proposal gate (`propose_*`, `confirm_proposal`), add_task richness, auto-propose-concept on add_knowledge
- ✅ **Phase B2**: workspace_id plumbing (env-driven scoping across every read + write)
- ✅ **Phase C**: storage interface lift + STORAGE_BACKEND switch (SQLite implementation TBD)
- ✅ **Phase D**: open source readiness (this README + LICENSE + goreleaser)

## Running locally

### Prerequisites

- Go 1.25.0+
- PostgreSQL 14+ with `pgvector` and `uuid-ossp` extensions
- (optional) `sqlc` 1.31+ if regenerating database code
- (optional) Discord bot token + Gemini API key for the full pipeline

### Quick start (Postgres only)

```bash
# 1. Configure
cp .env.example .env  # fill in DATABASE_URL, API_KEY, etc.

# 2. Apply migrations (every *.up.sql in order)
for f in migrations/*.up.sql; do psql "$DATABASE_URL" -f "$f"; done

# 3. Build + run
cd build
task build-server
./bin/wayneblacktea-server -env ../.env
```

The MCP server binary is built the same way (`task build-mcp`) and is
registered in your editor's MCP config (e.g. `.mcp.json` for Claude Code).

## Environment variables

| Var | Purpose | Default |
|---|---|---|
| `DATABASE_URL` | Postgres DSN (`postgres://user:pass@host/db?sslmode=require`) | required |
| `API_KEY` | HTTP API bearer token | required |
| `STORAGE_BACKEND` | `postgres` (default) or `sqlite` (not yet implemented) | `postgres` |
| `WORKSPACE_ID` | UUID scoping every read + write to one workspace | unset = legacy unscoped mode |
| `USER_ID` | Identity tag (currently used as `proposed_by` attribution) | unset |
| `PORT` | HTTP server port | `8080` |
| `ALLOWED_ORIGINS` | CORS allowlist (`*` or comma list) | `*` |
| `GEMINI_API_KEY` | Gemini embedding API for vector search + dedup | unset = dedup falls back to URL-only |
| `GROQ_API_KEY` | Groq for Discord bot LLM analysis | unset |
| `DISCORD_BOT_TOKEN` | Discord bot session token | unset = bot disabled |
| `DISCORD_GUILD_ID` | Discord guild for slash command registration | unset |
| `NOTION_INTEGRATION_SECRET` | Notion sync target | unset = sync_to_notion disabled |
| `NOTION_DATABASE_ID` | Notion database ID for synced pages | unset |

## Workspace isolation (Phase B2)

Setting `WORKSPACE_ID` to a UUID scopes every store to that workspace:

- Reads filter `WHERE workspace_id = $1`
- Writes populate `workspace_id` on insert
- Existing rows with `NULL` workspace_id remain invisible (run a backfill
  migration to assign them, e.g. `migrations/000011_backfill_workspace_id.sql`
  scaffold, then customise the UUID before applying)

Leaving `WORKSPACE_ID` unset preserves legacy behaviour: no filter, NULL on
insert. Single-user instances need not set it.

## MCP tool surface

29 tools across all domains. Notable ones:

- `get_today_context` (call at session start)
- `add_task` — supports `importance` (1-3) and free-form `context`
- `add_knowledge` — auto-proposes a concept card for review-eligible types
- `propose_goal` / `propose_project` / `confirm_proposal` — agent suggestions awaiting user confirmation
- `confirm_plan` — atomic multi-phase plan confirmation (creates tasks + decisions in one call)
- `set_session_handoff` / `resolve_handoff` — cross-session continuity

Run `initial_instructions` from any MCP client to see the usage protocol.

## Releases

Pre-built binaries are produced by GoReleaser (`.goreleaser.yml`) for:

- macOS (arm64, amd64)
- Linux (arm64, amd64)

Each release ships `wayneblacktea-server` and `wayneblacktea-mcp` binaries.

## Contributing

See [CONTRIBUTING.md](./CONTRIBUTING.md). Short version: feature/<slug> branch,
6-element dispatch prompts, `task check` 0 issues before commit.

## License

[MIT](./LICENSE).
