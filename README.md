<p align="center">
  <img src="docs/wayneblacktea.png" alt="wayneblacktea" width="320">
</p>

<p align="center">
  <strong>English</strong> &nbsp;·&nbsp; <a href="./README.zh-TW.md"><strong>繁體中文</strong></a>
</p>

<p align="center">
  <a href="./LICENSE"><img src="https://img.shields.io/badge/license-MIT-8C2A1A.svg" alt="MIT License"></a>
</p>

<p align="center">
  A personal-OS server for AI agents — your goals, decisions, knowledge,
  and learning live in one shared brain so the AI you work with already
  knows your context instead of asking you to re-explain it every
  conversation.
</p>

---

## Why this exists

Most AI workflows are stateless. Every chat starts from zero, every
agent is amnesiac, and you spend the day re-pasting links and
explaining yesterday's context. The more agents you add — an editor
assistant, a Discord helper, a daily summariser — the worse it gets.
You become the only piece of memory in the system.

wayneblacktea takes the opposite position: model your work as
**structured data** — goals, projects, tasks, decisions, knowledge,
concept cards, agent proposals, session handoffs — and let every
agent read and write the same store. The AI you work with already
knows your context. You stop being the clipboard.

## What this enables

- **Editor, Discord, and dashboard agree on state.** Save a link in
  Discord, see it on the dashboard a second later. No "wait did I
  tell you about this".
- **Saved knowledge feeds the review queue.** When you file an
  article or a TIL, the system drafts a spaced-repetition card for
  it. The queue grows from your reading habit, not from extra effort.
- **Decisions are queryable.** Architectural choices, tradeoffs,
  alternatives — all in one log. Six weeks later "why did I do X
  this way" returns a real answer.
- **Agent proposals stay proposals.** Anything an agent suggests
  with permanent consequences goes into a pending queue. You confirm
  or reject. Ownership of your agenda stays with you.
- **Cross-session continuity.** "Next time I'll keep working on Y"
  is a structured note the next session sees first.
- **Anti-amnesia signals.** The server tracks tool-call patterns
  and surfaces hints when something is being forgotten — stuck
  in-progress tasks, pending proposals piling up, decisions logged
  without a session-start recall.

## How it is organised

Seven bounded contexts. Each owns a slice of the model and a
narrowly-defined vocabulary; conflating them breaks the system.

| Context | What it owns |
|---|---|
| **GTD** | Goals → projects → tasks (with importance and discussion context), plus an activity log. |
| **Decisions** | Architectural and design decisions with rationale and alternatives. |
| **Knowledge** | Articles, TILs, bookmarks, Zettelkasten notes — full-text and semantic search, deduplicated at ingest. |
| **Learning** | Spaced-repetition concept cards on an FSRS schedule. The system can auto-propose cards from saved knowledge. |
| **Sessions** | Cross-session handoff notes — "what to continue next time". |
| **Proposals** | Agent-originated entities awaiting user confirmation. |
| **Workspace** | Tracked Git repos with status, known issues, and the next planned step. |

Every entity carries an optional workspace scope so multiple
isolated personal stores can share the same instance.

## Design philosophy

**Structure over prompts.** Encode the parts of your life you want
the AI to remember as explicit schema. No drift between agents, no
"I think you mentioned…", just the data.

**The user keeps the call.** Agents propose; you confirm. The
friction is the point — a system that decides for you eventually
makes you worse at deciding.

**Make forgetting visible.** Even disciplined agents forget to
close out work. Rather than hoping, the server records every tool
call and names the patterns out loud — the next session sees the
leftovers before the user types.

**Workflow tools, not raw CRUD.** The agent surface offers verbs —
*get today's context*, *confirm a plan*, *log a decision* — not raw
SELECTs. Rules live in the tool layer, not in prompt instructions
scattered across clients.

## What this is *not*

- **Not a team product.** One human, many agents. No RBAC, no shared
  workspaces, no Notion-clone collaboration.
- **Not a hosted service.** No multi-tenant SaaS. If you fork to
  self-host for yourself, the workspace scope keeps your data
  isolated; nothing more.
- **Not a stable API.** Built and run by one person. Releases are
  irregular, breaking changes will happen, the dashboard is unstyled
  in places.
- **Not a chatbot with memory.** The schema is the memory. Chat
  history is irrelevant.

---

## Installation

### Prerequisites

| Tool | Minimum version |
|------|----------------|
| Go | 1.26 |
| Node.js | 22 |
| Task | latest stable |
| PostgreSQL + pgvector | 14+ (see note below) |

> SQLite is also supported as a zero-infra alternative. Set `STORAGE_BACKEND=sqlite` and `SQLITE_PATH=/path/to/data.db` to skip PostgreSQL entirely.

### Clone and build

```bash
git clone https://github.com/Wayne997035/wayneblacktea.git
cd wayneblacktea

# Build the Go server binary
cd build && task build-server && cd ..

# Build the React dashboard (output is embedded in the server binary)
cd web && npm ci && npm run build && cd ..
```

### Environment variables

Copy the example file and fill in the required values:

```bash
cp .env.example .env
```

**Required**

| Variable | Purpose |
|----------|---------|
| `API_KEY` | Bearer token that authenticates every `/api/*` request |
| `DATABASE_URL` | PostgreSQL connection string (not needed for SQLite) |
| `ALLOWED_ORIGINS` | Comma-separated list of allowed CORS origins (e.g. `http://localhost:5173`) |

**Storage**

| Variable | Purpose |
|----------|---------|
| `STORAGE_BACKEND` | `postgres` (default) or `sqlite` |
| `SQLITE_PATH` | Path to the SQLite database file (SQLite backend only) |
| `POSTGRES_INSECURE_TLS` | Set to `true` when using managed Postgres providers (Aiven, Railway) |

**Optional**

| Variable | Purpose |
|----------|---------|
| `PORT` | HTTP port (default `8080`) |
| `WORKSPACE_ID` | UUID for workspace scoping; unset for single-user mode |
| `USER_ID` | Identity tag used as attribution on agent-originated writes |
| `CLAUDE_API_KEY` | Enables AI summarisation and activity classification |
| `GEMINI_API_KEY` | Enables vector embeddings for knowledge dedup and similarity search |
| `GROQ_API_KEY` | Powers the Discord bot LLM analyser |
| `DISCORD_BOT_TOKEN` | Discord bot session token; bot is disabled when unset |
| `DISCORD_GUILD_ID` | Discord guild for instant slash command registration |
| `DISCORD_WEBHOOK_URL` | Outbound webhook for scheduled briefing posts |
| `NOTION_INTEGRATION_SECRET` | Enables the `sync_to_notion` tool |
| `NOTION_DATABASE_ID` | Target Notion database for synced pages |

**Frontend build variable**

The dashboard is built once at compile time and embedded in the server binary. Before running `npm run build`, set:

| Variable | Purpose |
|----------|---------|
| `VITE_API_KEY` | Must match the server's `API_KEY` value |

```bash
cd web
VITE_API_KEY=your-api-key npm run build
```

### Run

```bash
./bin/wayneblacktea-server
# open http://localhost:8080
```

To load a custom env file:

```bash
./bin/wayneblacktea-server -env /path/to/.env
```

### Docker

```bash
docker build \
  --build-arg VITE_API_KEY=your-api-key \
  -f build/Dockerfile \
  -t wayneblacktea .

docker run \
  -p 8080:8080 \
  -e API_KEY=your-api-key \
  -e DATABASE_URL=postgres://user:pass@host/db?sslmode=require \
  -e ALLOWED_ORIGINS=http://localhost:8080 \
  wayneblacktea
```

---

The day-by-day log of what changes lives in [CHANGELOG.md].
Full self-hosting reference (migrations, workspace scoping, deployment) lives in [docs/installation.md].

Released under [MIT](./LICENSE).

[docs/installation.md]: ./docs/installation.md
[CHANGELOG.md]: ./CHANGELOG.md
