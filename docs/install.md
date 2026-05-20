# wayneblacktea install guide

## Quick start (recommended)

```bash
go install github.com/Wayne997035/wayneblacktea/cmd/wbt@latest
wbt init    # SQLite default, writes .env + .mcp.json
```

Open Claude Code from the directory containing the generated `.mcp.json`; it will start `wbt mcp` automatically after you approve the project MCP server. Verify with `> get_today_context`.

`wbt init` is an interactive wizard: picks SQLite (default, zero infra) or Postgres, generates an `API_KEY`, and writes `.env` + `.mcp.json`. No Anthropic API key is required for core MCP memory features.

**Want the web dashboard / HTTP MCP transport?** Also build the server binary, then start it:

```bash
go build -o "$(go env GOPATH)/bin/wayneblacktea-server" ./cmd/server
wbt serve                                                            # http://localhost:8080
claude mcp add --transport http wayneblacktea http://localhost:8080/mcp
```

## Coming soon (Phase 2 + 3 preview)

- **Phase 2 — `wbt setup`** one-command install with auto-start + MCP registration (in development)
- **Phase 3 — Homebrew tap**: `brew install wayne997035/tap/wayneblacktea`
- **Phase 3 — DXT package** for Claude Desktop one-click install

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
./bin/wbt init                     # choose [2] Postgres, enter DATABASE_URL
for f in migrations/0000*.up.sql; do psql "$DATABASE_URL" -f "$f"; done
cd build && task build-server build-mcp && cd ..
./bin/wbt serve
```

Required environment variables (server / PG mode):

| Variable | Purpose |
|----------|---------|
| `DATABASE_URL` | Postgres DSN (`postgres://user:pass@host/db?sslmode=require`) |
| `API_KEY` | Bearer token for every `/api/*` route. Generate with `openssl rand -hex 32` |
| `ALLOWED_ORIGINS` | Comma-separated CORS origins; must be explicit in production (`*` panics at startup) |

Optional: `STORAGE_BACKEND` (`sqlite`/`postgres`, default `sqlite`), `WORKSPACE_ID` (UUID scoping reads/writes), `USER_ID` (`proposed_by` attribution), `PORT` (default `8080`), `PGSSLROOTCERT` (CA cert path for Aiven / non-system trust store).

Optional integrations (each degrades gracefully when unset): `GEMINI_API_KEY` (vector embeddings), `GROQ_API_KEY` (Discord `/analyze`), `DISCORD_BOT_TOKEN` + `DISCORD_GUILD_ID` (Discord bot), `NOTION_INTEGRATION_SECRET` + `NOTION_DATABASE_ID` (`sync_to_notion`).

Setting `WORKSPACE_ID` filters every domain query with the workspace predicate and populates `workspace_id` on insert. Existing rows with `NULL` workspace_id become invisible — see [`operations.md`](operations.md#backfill-workspace_id-one-time-per-environment) for the backfill runbook.

## Docker

```bash
git clone https://github.com/Wayne997035/wayneblacktea.git
cd wayneblacktea
cp .env.example .env             # set API_KEY, DATABASE_URL, ALLOWED_ORIGINS

API_KEY=$(grep ^API_KEY .env | cut -d= -f2)
docker build --build-arg VITE_API_KEY="${API_KEY}" -f build/Dockerfile -t wayneblacktea .
docker run --rm -p 8080:8080 --env-file .env wayneblacktea
```

Three-stage build: Node 22-alpine builds the React dashboard → golang:1.26-alpine builds the Go binary with the dashboard embedded → alpine:3.21 runtime (non-root, `ca-certificates` only). Healthcheck: `GET /health` returns `200 OK`.

## Railway / production deploy

The canonical production deployment is a single Railway service built from `build/Dockerfile.server`. The database is managed Postgres with pgvector.

```bash
railway link --service <your service name>
railway up --ci -m "your message"
```

Tagged GitHub Releases produce cross-compiled binaries via `.goreleaser.yml` (linux/darwin × amd64/arm64).

## Build from source

Prerequisites: Go 1.26+, Node.js 22+, [Task](https://taskfile.dev/) (`go install github.com/go-task/task/v3/cmd/task@latest`), and PostgreSQL 14+ with pgvector (Postgres mode only).

```bash
cd build
task check                 # lint + tests + build (~30 s) — the gate
task build-server          # HTTP API + Discord bot + scheduler
task build-mcp             # MCP stdio server
task build-doctor          # wbt-doctor (Stop hook, writes /tmp/wbt-health.json)
go run ../cmd/seed         # first-time canonical goals + repos
```

## Pre-built release binaries + cosign verification

Release binaries are signed with [cosign](https://docs.sigstore.dev/cosign/overview/) keyless signing via GitHub OIDC. To verify a downloaded binary:

```bash
# Install cosign: https://docs.sigstore.dev/cosign/system_config/installation/

cosign verify-blob \
  --certificate-identity-regexp "https://github.com/Wayne997035/wayneblacktea/.github/workflows/release.yml@refs/tags/.*" \
  --certificate-oidc-issuer "https://token.actions.githubusercontent.com" \
  --signature wayneblacktea_checksums.txt.sig \
  --certificate wayneblacktea_checksums.txt.pem \
  wayneblacktea_checksums.txt
```

The `.sig` and `.pem` files are attached to each GitHub Release alongside the binaries. The identity regex is anchored to the release workflow on a semver tag; verification is fail-closed.

### Scripted install (curl | bash / irm | iex)

Installer scripts download, cosign-verify, and place binaries + a starter `.env` / `.mcp.json`. See the foot-gun warning below before piping to a shell.

```bash
# macOS / Linux
curl -fsSL https://raw.githubusercontent.com/Wayne997035/wayneblacktea/master/scripts/install.sh | bash
```

```powershell
# Windows / WSL
irm https://raw.githubusercontent.com/Wayne997035/wayneblacktea/master/scripts/install.ps1 | iex
```

**IMPORTANT — empty API_KEY foot-gun**: when piping to `bash` there is NO interactive wizard — stdin is the `curl` process, not your terminal. The script writes an EMPTY `API_KEY=` line and you MUST edit `~/.config/wayneblacktea/.env` before starting the server. To use the wizard, save the script first then run it directly.

Environment overrides:

| Env var | Purpose |
|---------|---------|
| `WBT_VERSION` | Pin to a specific release (default: latest) |
| `WBT_PREFIX` | Install prefix (default: `$HOME/.local`) |
| `WBT_CONFIG` | Config dir (default: `$HOME/.config/wayneblacktea`) |
| `WBT_NO_MCP` | Skip `claude mcp add` registration |
| `WBT_NO_PROMPT` | Force non-interactive (placeholders, empty API_KEY) |
| `WBT_INSECURE_SKIP_VERIFY` | Skip cosign verification (NOT RECOMMENDED — default is fail-closed) |

Server-side runtime flag: `WBT_DISABLE_AUTO_DECISIONS=1` (or `true`/`yes`/`on`) opts out of the MCP middleware that drafts pending decision proposals from observed tool calls (default on). Phase 3 will rewrite `install.sh` to delegate to `wbt setup` for end-to-end UX.

## Security notes

- **Never commit `.env`** — it is in `.gitignore`. Verify before every `git add`.
- **`API_KEY`** gates every `/api/*` route. Use ≥32 random chars (`openssl rand -hex 32`).
- **`ALLOWED_ORIGINS`** must be explicit origins, not `*` (panics at startup).
- **`VITE_API_KEY`** is baked into the frontend bundle at build time. It matches `API_KEY` — regenerate both together if compromised.
- **API keys in production** must be set as environment variables, never hardcoded.

See [`docs/ci-secrets.md`](./ci-secrets.md) for CI/CD secret management.

## Upgrading

```bash
git pull
for f in migrations/0000*.up.sql; do psql "$DATABASE_URL" -f "$f"; done
cd build && task build-server build-mcp && cd ..
```

Migrations are idempotent — running already-applied migrations is safe.

## Uninstall

```bash
# Remove binaries (adjust paths if installed via go install)
rm -f "$(go env GOPATH)/bin/"{wbt,wayneblacktea-server,wayneblacktea-mcp,wbt-doctor}

# Remove config (only if you want to drop your data)
rm -rf ~/.wayneblacktea ~/.config/wayneblacktea

# Remove MCP registration
claude mcp remove wayneblacktea
```
