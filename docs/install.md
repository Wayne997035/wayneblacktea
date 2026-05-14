# Installation guide

This document covers all installation modes and security notes.
For the self-hosting reference (migrations, workspace scoping, Railway deployment) see [`installation.md`](./installation.md).

## Mode 0: One-line install (pre-built binaries)

Best for: trying wayneblacktea without cloning the repo or installing Go/Node toolchains. Pulls the latest GitHub release of `wayneblacktea-mcp` plus the five CLI binaries (`wbt`, `wbt-context`, `wbt-hook`, `wbt-guard`, `wbt-doctor`) for your platform.

> **WARNING — `curl | bash` skips the wizard.** When you pipe the script to `bash`, stdin is the curl process (not your terminal), so the script falls back to non-interactive mode and writes an **empty** `API_KEY=` line. The server will refuse to start until you edit `~/.config/wayneblacktea/.env` and set a real key (`openssl rand -hex 32`). To use the interactive wizard, save the script to disk first and run it from a terminal:
>
> ```bash
> curl -fsSL https://raw.githubusercontent.com/Wayne997035/wayneblacktea/master/scripts/install.sh -o install.sh
> bash install.sh
> ```
>
> The same applies to the PowerShell installer below: `irm | iex` is non-interactive — save the script first if you want the wizard.

### macOS / Linux / WSL

```bash
curl -fsSL https://raw.githubusercontent.com/Wayne997035/wayneblacktea/master/scripts/install.sh | bash
```

What it does:

1. Detects OS (`linux` or `macos`) and arch (`x86_64` or `arm64`)
2. Resolves the latest release tag from the GitHub API (or uses `WBT_VERSION` if set)
3. Downloads `wayneblacktea-mcp_<ver>_<os>_<arch>.tar.gz` and `wayneblacktea-cli_<ver>_<os>_<arch>.tar.gz` plus `checksums.txt` and `checksums.txt.sig` / `.pem`
4. **Verifies the cosign keyless signature on `checksums.txt` (fail-closed by default).** Cosign + `.sig` + `.pem` MUST all be present and the signature MUST verify; otherwise installation aborts. To bypass (NOT RECOMMENDED), set `WBT_INSECURE_SKIP_VERIFY=1`.
5. Verifies SHA256 of every archive against `checksums.txt`
6. Inspects each archive for unsafe paths (absolute, `..` segments, `~`-prefixed) AND rejects symlink/hardlink entries before extraction (zip-slip + symlink-attack defence)
7. Extracts `wayneblacktea-mcp`, `wbt`, `wbt-context`, `wbt-hook`, `wbt-guard`, `wbt-doctor` to `~/.local/bin` (mode 0755)
8. Prompts (silent input for `API_KEY`) for `DATABASE_URL` (optional — blank means SQLite local file), `API_KEY` (required), and `WORKSPACE_ID` (optional), and writes them to `~/.config/wayneblacktea/.env` (mode 0600)
9. Runs `claude mcp add wayneblacktea -- ~/.local/bin/wayneblacktea-mcp` if the `claude` CLI is installed; otherwise prints the command to run later

Environment overrides:

| Variable | Default | Purpose |
|----------|---------|---------|
| `WBT_VERSION` | latest | Pin a specific release (e.g. `1.2.3`); validated against `MAJOR.MINOR.PATCH[-prerelease]` before URL interpolation |
| `WBT_PREFIX` | `~/.local` | Install prefix; binaries land in `$WBT_PREFIX/bin` |
| `WBT_CONFIG` | `~/.config/wayneblacktea` | Config directory |
| `WBT_NO_MCP` | `0` | Set to `1` to skip `claude mcp add` |
| `WBT_NO_PROMPT` | `0` | Set to `1` to skip the interactive wizard (writes empty `API_KEY` you must edit before starting the server) |
| `WBT_INSECURE_SKIP_VERIFY` | `0` | **Fail-closed cosign verification is the default.** Set to `1` ONLY if you cannot install cosign (e.g. air-gapped env). NOT RECOMMENDED for production. Replaces the old `WBT_STRICT_VERIFY` opt-in (semantic flip). |
| `WBT_DISABLE_AUTO_DECISIONS` | `0` (enabled) | Auto-decision proposer drafts a `pending_proposals` row (type=decision) after every mutating MCP tool when no `log_decision`/`confirm_plan` happened in the last 15 min. Default is **ON** because wayneblacktea's purpose is to auto-track decisions; spam is mitigated by the proposal-queue confirmation step. Set to `1` to opt out (e.g. tight LLM budget). |

Hardening notes:

- Script is `set -euo pipefail`, validates every download against the published `checksums.txt` (SHA256), and refuses to overwrite a newer existing `wbt` (compare via `wbt version`, second whitespace token).
- `cosign verify-blob` runs against `checksums.txt.sig` before checksums are parsed, pinning identity to the **anchored** regex `^https://github\.com/Wayne997035/wayneblacktea/\.github/workflows/release\.yml@refs/tags/v[0-9]+\.[0-9]+\.[0-9]+$` and OIDC issuer `https://token.actions.githubusercontent.com` (goreleaser-keyless). Cosign + `.sig` + `.pem` are mandatory; an attacker who strips or 404-spoofs them aborts the install.
- Tar archives are scanned for absolute paths, `..` segments, `~`-prefixed entries, **and symlink / hardlink entries** (`tar -tzvf` long-listing mode column = `l` / `h`) before extraction; extraction uses `--no-same-owner --no-same-permissions`.
- API key is read with `read -rs` (silent input) so the secret is not echoed on screen.
- Uses `mktemp -d` for staging and traps `EXIT INT TERM` for cleanup.
- Warns when `~/.local/bin` is not on your PATH and prints the exact line to add to `~/.zshrc` / `~/.bashrc` / fish config.

### Windows (PowerShell 5.1+ or pwsh 7+)

```powershell
irm https://raw.githubusercontent.com/Wayne997035/wayneblacktea/master/scripts/install.ps1 | iex
```

What it does:

1. Detects arch via `[System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture` (`X64` -> `x86_64`, `Arm64` -> `arm64`)
2. Resolves the latest release tag via `Invoke-RestMethod`
3. Downloads `wayneblacktea-mcp_<ver>_windows_<arch>.zip` and `wayneblacktea-cli_<ver>_windows_<arch>.zip` plus `checksums.txt` and `checksums.txt.sig` / `.pem`
4. **Verifies the cosign keyless signature on `checksums.txt` (fail-closed by default).** Cosign + `.sig` + `.pem` MUST all be present and the signature MUST verify; otherwise installation aborts. To bypass (NOT RECOMMENDED), set `$env:WBT_INSECURE_SKIP_VERIFY = '1'`.
5. Verifies SHA256 with `Get-FileHash` before extraction
6. Inspects each ZIP entry via `[System.IO.Compression.ZipFile]::OpenRead` and rejects unsafe paths (absolute, `..` segments, drive letters, leading backslash, `~`-prefixed) before `Expand-Archive`
7. Extracts six binaries (`wayneblacktea-mcp.exe`, `wbt.exe`, `wbt-context.exe`, `wbt-hook.exe`, `wbt-guard.exe`, `wbt-doctor.exe`) to `%LOCALAPPDATA%\wayneblacktea\bin` and prepends that directory to the user `PATH` (`[Environment]::SetEnvironmentVariable('Path', ..., 'User')`)
8. Prompts for `DATABASE_URL` and `API_KEY` (read with `Read-Host -AsSecureString` so the secret is not echoed) and writes `%LOCALAPPDATA%\wayneblacktea\config\.env` with a current-user-only NTFS ACL (inheritance disabled)
9. Runs `claude mcp add wayneblacktea -- "<install dir>\wayneblacktea-mcp.exe"` if the `claude` CLI is installed

Environment overrides (set before piping to `iex`):

```powershell
$env:WBT_VERSION              = '1.2.3'  # pin a release (validated against semver before URL use)
$env:WBT_NO_MCP               = '1'      # skip claude registration
$env:WBT_NO_PROMPT            = '1'      # skip wizard, write empty API_KEY
$env:WBT_INSECURE_SKIP_VERIFY = '1'      # bypass cosign (NOT RECOMMENDED — default is fail-closed)
```

Hardening notes:

- `$ErrorActionPreference = 'Stop'` and `Set-StrictMode -Version Latest` enforce hard-fail behavior.
- TLS 1.2 is forced on Windows PowerShell 5.x for compatibility with GitHub.
- `cosign verify-blob` runs against `checksums.txt.sig` **fail-closed by default** — missing cosign / missing `.sig` / missing `.pem` / verification failure all abort the install. `$env:WBT_INSECURE_SKIP_VERIFY = '1'` is the only escape hatch and is not recommended. Identity regex is anchored (`^...$`) to the release.yml workflow on a semver tag.
- ZIP entries are validated against a path-traversal regex via `System.IO.Compression.ZipFile` before `Expand-Archive`; mismatched archive aborts with exit 2.
- API key input uses `Read-Host -AsSecureString`; the value is decrypted in-memory only long enough to write the file, then the local variable is cleared.
- The `.env` file ACL is reset (inheritance disabled, only current user keeps `FullControl`).

### Verifying the install

```bash
wbt version          # prints "<binary> <version> (<commit>)"
wbt --help
ls -la ~/.config/wayneblacktea/.env  # macOS/Linux: should be -rw------- (0600)
```

```powershell
wbt version
Get-Acl "$env:LOCALAPPDATA\wayneblacktea\config\.env" | Format-List
```

All shipped binaries (`wbt`, `wbt-context`, `wbt-hook`, `wbt-guard`, `wbt-doctor`, `wayneblacktea-mcp`) accept `version` / `--version` and emit the same one-line format.

### Uninstall

```bash
rm ~/.local/bin/{wayneblacktea-mcp,wbt,wbt-context,wbt-hook,wbt-guard,wbt-doctor}
rm -rf ~/.config/wayneblacktea   # only if you want to remove config too
claude mcp remove wayneblacktea  # if it was registered
```

```powershell
Remove-Item "$env:LOCALAPPDATA\wayneblacktea" -Recurse
claude mcp remove wayneblacktea
```

## Prerequisites (Modes 1-3, source builds)

Mode 0 (one-line install) requires no prerequisites — pre-built binaries are downloaded directly. The table below applies only to building from source.

| Tool | Minimum version | Notes |
|------|----------------|-------|
| Go | 1.26 | Required to build all binaries |
| Node.js | 22 | Required to build the React dashboard |
| Task | latest stable | `go install github.com/go-task/task/v3/cmd/task@latest` |
| PostgreSQL + pgvector | 14+ | Postgres mode only |

Optional integrations (none are required to start the server):

| Integration | Variable | Degrades to |
|-------------|----------|-------------|
| Claude AI | `CLAUDE_API_KEY` | No advanced automation: AI activity classification, reflection, snapshots, or review suggestions |
| Gemini | `GEMINI_API_KEY` | No knowledge vector embeddings; knowledge dedup falls back to URL-only |
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
#   .mcp.json  (points Claude Code at wbt mcp)

# Build and start the server
cd build && task build-server build-mcp && cd ..
./bin/wbt serve
# Server runs on http://localhost:8080
```

What `wbt init` asks:

1. Database: `[1] SQLite` or `[2] Postgres`
2. For SQLite: local file path (default `~/.wayneblacktea/data.db`)
3. Server port (default `8080`)
4. `API_KEY` -- auto-generates a random key if you press Enter

No AI provider key is required for core MCP memory features. Add optional provider keys to `.env` later only when enabling the integrations above.

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
  wayneblacktea
```

## Connecting Claude Code (MCP)

`wbt init` writes `.mcp.json` in the project root. Open Claude Code from that directory and it picks up the MCP server automatically.

For manual setup, `.mcp.json` has this shape:

```json
{
  "mcpServers": {
    "wayneblacktea": {
      "command": "wbt",
      "args": ["mcp"],
      "env": {
        "STORAGE_BACKEND": "sqlite",
        "SQLITE_PATH": "/path/to/data.db"
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
- **`PGSSLROOTCERT`** -- if your Postgres CA is not in the system trust store, point this variable at your CA certificate file.
- **`VITE_API_KEY`** is baked into the frontend bundle at build time. It matches `API_KEY` -- regenerate both together if compromised.

See [`docs/ci-secrets.md`](./ci-secrets.md) for CI/CD secret management.

## Upgrading

```bash
git pull
for f in migrations/0000*.up.sql; do psql "$DATABASE_URL" -f "$f"; done
cd build && task build-server build-mcp && cd ..
```

Migrations are idempotent -- running already-applied migrations is safe.
