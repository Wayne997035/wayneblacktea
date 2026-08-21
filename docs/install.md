# wayneblacktea install guide

## System requirements

| Item | Minimum |
|------|---------|
| macOS | 13+ (lsof + setsid compatibility) |
| Linux | kernel 4.18+, glibc 2.28+ (for ss fallback and nohup) |
| Windows | Not supported in Phase 2 — the lifecycle package returns `ErrUnsupported`; use `wbt mcp` (stdio) manually |
| Go | 1.26+ (for `go install` path) |
| Node.js | 22+ (build-from-source only) |
| Task | latest (build-from-source only — `go install github.com/go-task/task/v3/cmd/task@latest`) |

## Quick start

```bash
go install github.com/Wayne997035/wayneblacktea/cmd/wbt@latest
wbt setup
```

`wbt setup` is the one-command install (shipped in Phase 2). It does end-to-end:

1. Reads or creates global config (macOS: `~/Library/Application Support/wayneblacktea/config.yaml`; Linux: `~/.config/wayneblacktea/config.yaml`; mode 0600).
2. Ensures the SQLite directory exists (default backend, zero infra).
3. Resolves the HTTP port (`WBT_PORT` env > `--port` flag > config file > default `8420`). When deploying to a PaaS like Railway, the platform injects `$PORT` which overrides this default.
4. Probes `/health` on the resolved port; if a healthy wayneblacktea server is already there, reuses it.
5. Otherwise, reclaims the TCP port (kills the occupier if needed) and spawns `wayneblacktea-server` in the background via `nohup`, writing the PID file to `$XDG_STATE_HOME/wayneblacktea/server.pid` (default `~/.local/state/wayneblacktea/`).
6. Polls `/health` until the new server reports ready (15 s deadline).
7. Calls `claude mcp add wayneblacktea --transport http http://127.0.0.1:<port>/mcp` so any Claude Code session can find it.

### First-run walkthrough

Successful `wbt setup` output looks like this (step names are from the source; pid and port will differ):

```
$ wbt setup
==> Reading or creating config…
  [ok] Config ready
==> Ensuring SQLite directory…
  [ok] SQLite directory ready
==> Resolving port…
  [ok] Port resolved: 8420
==> Checking for an existing healthy server…
==> Reclaiming TCP port if occupied…
  [ok] Port is free
==> Spawning wayneblacktea-server in the background…
  [ok] Server spawned (pid 12345, logs ~/.local/state/wayneblacktea/server.log)
==> Waiting for /health…
  [ok] Server is healthy
==> Registering MCP with Claude Code…
  [ok] claude mcp add wayneblacktea --transport http http://127.0.0.1:8420/mcp

All set. wayneblacktea is running at http://127.0.0.1:8420

Next commands:
  wbt status        - show server state
  wbt stop          - stop the background server
  wbt restart       - stop + setup
```

Run `wbt status` at any time to confirm the port the server is actually using.

Verify the install:

```bash
wbt status                      # shows pid / port / health
claude mcp get wayneblacktea    # should show ✔ Connected
```

### `wbt setup` flags

| Flag | Default | Purpose |
|------|---------|---------|
| `--port=<n>` | `WBT_PORT` env or `8420` | Override the HTTP port (the resolved value is persisted to config so `wbt status` reports correctly). |
| `--no-mcp` | off | Skip the `claude mcp add` step (useful if you manage MCP registrations manually). |
| `--mcp-name=<n>` | `wayneblacktea` | Override the name `claude mcp add` registers (useful if you run multiple wayneblacktea instances). |
| `--server-bin=<path>` | `exec.LookPath("wayneblacktea-server")` | Override the server binary location (intended for tests / non-standard installs). |

### Sister commands

| Command | What it does |
|---------|--------------|
| `wbt status` | Prints a one-line summary: `pid`, `port`, `transport`, `healthy`, `pid_file`, `started_at`. Exit 0 if running and healthy. |
| `wbt status --format json` | Same data as a JSON object — useful for shell scripts. |
| `wbt stop` | Terminates the background server identified by the PID file and removes the PID file. Idempotent (missing PID file → exits 0 with "not running"). |
| `wbt restart` | `wbt stop` followed by `wbt setup`. |
| `wbt mcp` | Run the MCP server over stdio in the foreground. Useful for legacy stdio MCP integrations or for `.mcp.json`-managed project servers. |
| `wbt init` | Deprecated alias for `wbt setup`; prints a deprecation notice on stderr but still runs. |

## Using it: dashboard, GTD setup, and auto-capture

### The local dashboard (SQLite, in your browser)

A local install uses **SQLite** — no database server — at `~/.local/share/wayneblacktea/wbt.db` (override with `WBT_DB_PATH`). The server embeds the React dashboard and serves it at the root path, so once `wbt setup` reports healthy, open:

```
http://localhost:8420/
```

(Use whatever port `wbt status` reports — `8420` is the local default.) The dashboard shows your GTD board, the **calendar** (tasks plotted by `due_date`), goals, projects, and weekly progress — all read from the same SQLite DB the MCP tools write to.

### First-run: getting your work into the GTD

The GTD/calendar starts empty — you populate it through the MCP tools from a Claude Code session (or the HTTP API):

1. **Register a project / repo** so tasks have a home:
   - `create_project` — create a GTD project (the unit tasks attach to via `project_id`).
   - `sync_repo` — record a git repository entry (`name`, `path`, `language`, `current_branch`, `next_planned_step`). Note: `sync_repo` stores the values you pass — it does **not** auto-scan the filesystem; you supply the metadata.
2. **Add tasks** — `add_task` (a `due_date` is **required**) → each task plots on the dashboard calendar by its due date.
3. **Log decisions / confirm plans** — `log_decision` or `confirm_plan` records the rationale next to the work.
4. **Open `http://localhost:8420/`** to see it on the board + calendar. Inside Claude Code, `get_today_context` (auto-called at session start) returns the same snapshot.

### When does it store things automatically?

Beyond what you explicitly create, the server persists work for you — all server-side, so it survives a misbehaving client:

- **Activity audit log** — these high-signal tools write an `activity_log` entry on success, regardless of client: `add_task` / `update_task` / `begin_task` / `complete_task`, `log_decision`, `confirm_plan`, `set_session_handoff`, `start_work` / `finish_work` / `checkpoint_work`, `reconcile_dashboard`. (Read-only tools such as `get_active_work` are not logged.)
- **Implicit decision drafting** — when you invoke a *significant* tool (`complete_task`, `confirm_proposal`, `upsert_project_arch`, `update_project_status`, `resolve_handoff`, `sync_repo`), a classifier observes the call and **drafts a pending decision proposal** (plus follow-up tasks) for you to confirm or reject later — so a decision isn't lost just because you forgot to call `log_decision`. On by default; set `WBT_DISABLE_AUTO_DECISIONS=1` to turn it off. It is rate-limited (60 classify calls/minute) and needs `ANTHROPIC_API_KEY` for the classifier.
- **Session handoff on stop** — the Stop hook auto-fires `set_session_handoff` at session end, capturing where you left off.

A *decision* itself is committed when you call `log_decision` / `confirm_plan`, or when you confirm an auto-drafted proposal — the middleware drafts and audits, it does not silently commit decisions without a confirm step.

## API keys

### Shared secret (auto-generated — required for all requests)

`wbt setup` auto-generates a 64-character hex shared secret (32 random bytes) and writes it to **both**:

- **Server config** (`api_key:` field in `config.yaml`, mode 0600) — the server reads this at startup and uses it as the expected value for every inbound `X-API-Key` header.
- **Claude Code MCP config** — registered automatically so Claude Code sends the correct header with each MCP request.

You never set this key manually. To verify it is wired correctly:

```bash
claude mcp get wayneblacktea    # should show ✔ Connected
```

**This key is a local-only access gate**: the server binds to `127.0.0.1` only, so no external traffic reaches it, but any local process could reach the port. The key prevents local tools from reading your personal-OS GTD/knowledge data without your consent.

**To rotate the key**: edit `api_key:` in `config.yaml` (macOS: `~/Library/Application Support/wayneblacktea/config.yaml`; Linux: `~/.config/wayneblacktea/config.yaml`), then run `wbt restart`. Claude Code's MCP registration must also be updated — re-run `wbt setup` after editing the config (it will re-register with the new key).

**Do not** paste, commit, or screenshot `config.yaml` — the key is plaintext.

**A `Claude API key not configured` line in `server.log` refers to the OPTIONAL Anthropic key below**, not this shared secret. MCP connectivity is not affected.

### Anthropic API key (optional — for AI features only)

**Not required for core MCP features**: GTD, decisions, knowledge search, session handoff, vision items, and proposals all work without it.

**Required for**: weekly reflection summarizer, AI knowledge atomization, and embedding generation (semantic vector search over knowledge).

Set via `ANTHROPIC_API_KEY` in your shell environment or in `config.yaml`. After updating, run `wbt restart`.

## MCP client compatibility

| Client | Transport | Setup |
|--------|-----------|-------|
| Claude Code | HTTP (auto-registered by `wbt setup`) | Run `wbt setup`; no further config needed |
| Claude Code (legacy stdio) | stdio | Use `wbt setup --no-mcp`, then add manually: `wbt mcp` as the command in `.mcp.json` |
| Claude Desktop | stdio | `go install …/cmd/wbt@latest`, then add `wbt mcp` as the command in Claude Desktop's MCP config. There is no DXT package — this project ships no binaries. |
| Cursor | HTTP | Manual config — run `wbt status --format json` to get the current URL, then point Cursor at `http://localhost:<port>/mcp` |
| Other MCP clients | varies | Run `wbt status --format json` to discover the current URL |

`wbt setup`'s remove-then-add only touches the `wayneblacktea` entry in Claude Code. All other registered MCP servers are untouched. Verify with `claude mcp list` after setup.

## Upgrading

```bash
go install github.com/Wayne997035/wayneblacktea/cmd/wbt@latest
wbt setup
```

`wbt setup` is idempotent: it re-registers the MCP entry with the current binary path and restarts the server if the PID file points to an exited process. To force a fresh server immediately: `wbt restart`.

Schema migrations run automatically when the server starts — there is no manual migration step for `go install` users.

For build-from-source upgrades:

```bash
git pull
cd build && task build-server build-wbt && cd ..
wbt restart
```

Migrations are idempotent — applying already-applied migrations is safe.

### Migration from earlier wbt

Earlier versions documented `wbt init` as the install entry point. `wbt init` now redirects to `wbt setup` (with a stderr deprecation notice) and there is no on-disk migration to perform — existing `.env` / `.mcp.json` files keep working. If you have an older stdio-only setup, run `wbt setup` once and it will register the HTTP MCP transport; remove the stdio entry afterwards with `claude mcp remove <old-name>` if you no longer need it.

## Privacy and architecture

- All data lives on your machine: SQLite file at `~/.local/share/wayneblacktea/wbt.db` (unless you configure Postgres).
- The server listens on `127.0.0.1` only — not `0.0.0.0`. No LAN or internet exposure.
- Anthropic API calls are made only when `ANTHROPIC_API_KEY` is set and an AI feature (reflection, atomization, embedding) is triggered.
- No telemetry, no analytics, no auto-update phone-home.
- `~/.local/state/wayneblacktea/server.log` is mode 0600 and may contain task titles and decision text from your sessions.

## Troubleshooting

**`wbt setup` exits "Port reclaim failed"**
Another process is holding the port and could not be killed (e.g. a system service). Find the port with `wbt status` then check it with `lsof -i :<port>`, kill the process manually, then re-run `wbt setup`. Alternatively, use a different port: `wbt setup --port 9090`.

**`claude CLI not found` during MCP registration**
`wbt setup` could not find the `claude` binary. Install Claude Code from https://claude.ai/download, then re-run `wbt setup`. If you prefer to register manually, copy the printed `claude mcp add` command from the setup output and run it yourself, or add the entry directly to `~/.claude/mcp_settings.json`.

**`/health` timeout during setup**
The server did not become healthy within 15 seconds. Check the log for a crash:

```bash
tail -50 ~/.local/state/wayneblacktea/server.log
```

Common causes: missing binary (`wayneblacktea-server` not in PATH), port conflict that was not reclaimed, or SQLite permission error.

**`wbt status` shows running but Claude Code does not see the MCP server**
The MCP URL registered with Claude Code may not match the running server. Compare:

```bash
wbt status --format json    # shows the URL the server is actually using
claude mcp list             # shows what Claude Code has registered
```

If they differ, re-run `wbt setup` to re-register with the current URL.

**Aiven Postgres SSL handshake failure**
Confirm `PGSSLROOTCERT` is set to the path of your `ca.pem` file and that the file has mode 0600:

```bash
chmod 0600 /path/to/ca.pem
export PGSSLROOTCERT=/path/to/ca.pem
```

`sslmode=no-verify` and an empty `PGSSLROOTCERT` do not work with this psql version — the certificate must be provided.

**Hook binary not found in PATH**
`wbt-doctor` (Stop hook) and `wbt-context` (SessionStart hook) must be on your PATH. The `go install` path puts binaries in `$(go env GOPATH)/bin` (typically `~/.local/bin` or `~/go/bin`). Check that directory is in your shell's PATH:

```bash
echo $PATH | tr : '\n' | grep -E 'local/bin|go/bin'
```

If missing, add the appropriate directory to your shell profile and restart your shell.

### Install channels

| Channel | Command | Notes |
|---------|---------|-------|
| **go install** (recommended) | `go install github.com/Wayne997035/wayneblacktea/cmd/wbt@latest && wbt setup` | Requires Go 1.26+. Works today. |
| **Build from source** | See [Build from source](#build-from-source) below | Requires Go 1.26+, Node.js 22+, Task. |

There is no pre-built-binary channel. Distribution is the git tag plus the Go module proxy —
`go install …@v1.0.0` fetches, verifies against the checksum database, and builds. Homebrew
casks, DXT packages, `curl | bash` installers, and cosign signing were all removed once
`go install` was shown to cover every case they were built for; the removed pipeline had
never successfully produced a release.

## Postgres (advanced)

Best for: full feature set including pgvector semantic search.

Your Postgres instance must have the `pgvector` extension:

```sql
CREATE EXTENSION IF NOT EXISTS vector;
```

Managed providers (Railway, Aiven, Supabase) have pgvector available — enable it from the dashboard or run the SQL above.

```bash
git clone https://github.com/Wayne997035/wayneblacktea.git
cd wayneblacktea
go build -o bin/wbt ./cmd/wbt
# Set DATABASE_URL in ~/.config/wayneblacktea/config.yaml or export it before running setup.
# wbt setup will use the DATABASE_URL env var and auto-detect the Postgres backend.
export DATABASE_URL="postgres://user:pass@host/db?sslmode=require"
cd build && task migrate-up && cd ..   # apply Postgres migrations
cd build && task build-server build-wbt && cd ..
./bin/wbt setup                        # spawns server in background, registers MCP
```

Required environment variables (server / PG mode):

| Variable | Purpose |
|----------|---------|
| `DATABASE_URL` | Postgres DSN (`postgres://user:pass@host/db?sslmode=require`) |
| `API_KEY` | Bearer token for every `/api/*` route. Generate with `openssl rand -hex 32` |
| `ALLOWED_ORIGINS` | Comma-separated CORS origins; must be explicit in production (`*` panics at startup) |

Optional: `STORAGE_BACKEND` (`sqlite`/`postgres`, default `sqlite`), `WORKSPACE_ID` (UUID scoping reads/writes), `USER_ID` (`proposed_by` attribution), `PORT` (default `8420` locally; PaaS platforms such as Railway inject this automatically), `PGSSLROOTCERT` (CA cert path for Aiven / non-system trust store).

Optional integrations (each degrades gracefully when unset): `GEMINI_API_KEY` (vector embeddings), `GROQ_API_KEY` (Discord `/analyze`), `DISCORD_BOT_TOKEN` + `DISCORD_GUILD_ID` (Discord bot), `NOTION_INTEGRATION_SECRET` + `NOTION_DATABASE_ID` (`sync_to_notion`).

Setting `WORKSPACE_ID` filters every domain query with the workspace predicate and populates `workspace_id` on insert. Existing rows with `NULL` workspace_id become invisible — see [`operations.md`](operations.md#backfill-workspace_id-one-time-per-environment) for the backfill runbook.

## Docker

```bash
git clone https://github.com/Wayne997035/wayneblacktea.git
cd wayneblacktea
cp .env.example .env             # set API_KEY, DATABASE_URL, ALLOWED_ORIGINS

API_KEY=$(grep ^API_KEY .env | cut -d= -f2)
docker build --build-arg VITE_API_KEY="${API_KEY}" -f build/Dockerfile -t wayneblacktea .
docker run --rm -p "${PORT:-8420}:${PORT:-8420}" --env-file .env wayneblacktea
```

Three-stage build: Node 22-alpine builds the React dashboard → golang:1.26-alpine builds the Go binary with the dashboard embedded → alpine:3.21 runtime (non-root, `ca-certificates` only). Healthcheck: `GET /health` returns `200 OK`.

## Railway / production deploy

The canonical production deployment is a single Railway service built from `build/Dockerfile.server`. The database is managed Postgres with pgvector.

```bash
railway link --service <your service name>
railway up --ci -m "your message"
```

A git tag is the release. There is no build pipeline behind it — the Go module proxy serves the
tagged source and `go install` builds for whatever platform the user is on, so no cross-compile
matrix has to be maintained or kept in sync with the platforms people actually run.

## Build from source

Prerequisites: Go 1.26+, Node.js 22+, [Task](https://taskfile.dev/) (`go install github.com/go-task/task/v3/cmd/task@latest`), and PostgreSQL 14+ with pgvector (Postgres mode only).

```bash
cd build
task check                 # lint + tests + build (~30 s) — the gate
task build-server          # HTTP API + Discord bot + scheduler (bin/wayneblacktea-server)
task build-wbt             # wbt CLI including `wbt mcp` stdio server (bin/wbt)
task build-doctor          # wbt-doctor (Stop hook, writes /tmp/wbt-health.json)
go run ../cmd/seed         # first-time canonical goals + repos
```

Note: there is no standalone `build-mcp` target — MCP stdio is served by `wbt mcp` (part of `build-wbt`).

## Verifying what you installed

`go install` already verifies. Every module the toolchain fetches is checked against its `h1:`
hash in the [Go checksum database](https://sum.golang.org), fail-closed — a tampered or
substituted module makes the install fail, with no flag to opt in and none to accidentally skip.
That is the same guarantee the removed cosign signing provided, except nobody has to run a
verify command for it to hold.

To see what a built binary actually is:

```bash
go version -m $(command -v wbt)
```

The `mod` line carries the module path, the version, and the `h1:` sum:

```
mod  github.com/Wayne997035/wayneblacktea  v1.0.0  h1:Sv3vlBd5HKOxG2ZbhSks8q2SxB11DLxV8BKwEq0/YXs=
```

`wbt version` reads the same build info, so a proxy-installed binary reports its real version
without any link-time injection.

## Security notes

- **Never commit `.env`** — it is in `.gitignore`. Verify before every `git add`.
- **`API_KEY`** gates every `/api/*` route. Use ≥32 random chars (`openssl rand -hex 32`).
- **`ALLOWED_ORIGINS`** must be explicit origins, not `*` (panics at startup).
- **`VITE_API_KEY`** is baked into the frontend bundle at build time. It matches `API_KEY` — regenerate both together if compromised.
- **API keys in production** must be set as environment variables, never hardcoded.

See [`docs/ci-secrets.md`](./ci-secrets.md) for CI/CD secret management.

## Uninstall

```bash
# Remove binaries (adjust paths if installed via go install)
rm -f "$(go env GOPATH)/bin/"{wbt,wayneblacktea-server,wayneblacktea-mcp,wbt-doctor}

# Remove config (only if you want to drop your data)
rm -rf ~/.wayneblacktea ~/.config/wayneblacktea

# Remove MCP registration
claude mcp remove wayneblacktea
```
