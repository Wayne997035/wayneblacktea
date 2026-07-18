# AGENTS.md

Working notes for AI coding agents (Codex, etc.) operating in this repo.
wayneblacktea is a single-tenant personal-OS MCP server: GTD tasks,
architecture decisions, knowledge, learning cards, and session handoffs for
one maintainer's AI workflows, exposed over HTTP REST + MCP (stdio and HTTP).

## Build / test

```bash
cd build && task check   # full quality gate: lint + unit + integration tests + frontend build — must be 0 issues before commit/PR
```

Other useful tasks from `build/`: `task lint`, `task test`, `task build-all`,
`task migrate-up` (needs `DATABASE_URL` in `.env.local`), `task sqlc`
(regenerate `internal/db/*.sql.go` after changing `sql/*.sql`).

## Hard rules

- **No FK constraints anywhere in the schema.** `migrations/*.sql` must never
  contain `FOREIGN KEY` / `REFERENCES` / `ON DELETE` / `ON UPDATE`. CI enforces
  this (`.github/workflows/ci.yml`, lint job). Referential integrity is
  application-layer only.
- **Existing migrations are immutable.** Once merged, a migration file may
  never be edited or deleted — only new migration files may be added. CI
  enforces this (`.github/workflows/migration-immutability.yml`).
- **Dual backend parity.** Every domain `Store` interface must be implemented
  identically for both SQLite (`internal/storage/sqlite/`) and Postgres+pgvector
  (`internal/db/`, via sqlc). Adding a method to one backend without the other
  breaks `storage.ResolveFromEnv()`.
- **Proposals gate.** Agent-originated durable writes (new knowledge, tasks,
  decisions inferred by automation rather than confirmed by the user) go
  through the `proposal` domain for user confirmation — they don't write
  directly to primary tables.

## Directory map

- `cmd/` — binary entrypoints: `server` (HTTP+MCP), `discordbot`, `wbt` (CLI
  installer), `mcp` (stdio MCP), `wbt-context`/`wbt-hook`/`doctor` (Claude Code
  hooks), `seed`.
- `internal/{gtd,decision,session,knowledge,proposal,workspace,learning,...}` —
  domain packages, each owning its entity + `Store` interface + handler.
- `internal/storage/sqlite/`, `internal/db/` — the two storage backends.
- `web/` — React dashboard (embedded into the server binary via `go:embed`).
- `migrations/` — numbered Postgres SQL migrations (immutable once merged).
- `build/` — `Taskfile.yml` (all dev commands) and the production `Dockerfile`.
- `docs/` — architecture, API, ops, and security reference (see below);
  `docs/internal/` holds planning/historical docs, not current-state docs.

## Where to read more

- `README.md` — install, feature overview, design principles.
- `docs/architecture.md` — system design deep-dive.
- `docs/api.md` — REST endpoint reference.
- `docs/mcp-tools.md` — full MCP tool reference.
- `CLAUDE.md` (repo root, gitignored/local) — the maintainer's fuller working
  rules; read it if present, but it is not required to be in your checkout.
