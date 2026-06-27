# Troubleshooting

Common setup failures for new instances.

---

## `wbt serve` / server won't start

**`API_KEY not set`**

The server exits at startup if `API_KEY` is empty. `wbt setup` auto-generates a 64-char hex key and writes it to `config.yaml` (macOS: `~/Library/Application Support/wayneblacktea/config.yaml`; Linux: `~/.config/wayneblacktea/config.yaml`) automatically — you should never need to set it manually. If running the server binary directly without `wbt setup`, set `API_KEY` in your `.env` file or environment.

> **Note**: a `Claude API key not configured` line in `server.log` refers to the OPTIONAL `ANTHROPIC_API_KEY` (for AI summarization features), not this `API_KEY`. MCP connectivity is unaffected if `ANTHROPIC_API_KEY` is absent.

**`ALLOWED_ORIGINS` panic**

`CORSMiddleware` panics at startup when `ALLOWED_ORIGINS` is `"*"` or empty after resolution. In local dev the server defaults to `http://localhost:<PORT>,http://127.0.0.1:<PORT>` — this only works when `APP_ENV` is not set to `production`. In production you must set `ALLOWED_ORIGINS` explicitly:

```
ALLOWED_ORIGINS=https://your-domain.example.com
```

**Database connection refused**

`STORAGE_BACKEND` controls which backend is used. When unset it defaults to `sqlite` — no running database required. Set `STORAGE_BACKEND=postgres` only when a Postgres server is reachable and `DATABASE_URL` is set.

**Port already in use**

The server defaults to port `8420`. `wbt setup` uses `WBT_PORT` env > `--port` flag > config file > `8420`. To use a different port: `wbt setup --port 9090`. For direct server invocation, set `PORT=<number>` in `.env`.

---

## SQLite backend issues

**Where the file is created**

`wbt setup` defaults the SQLite path to `~/.local/share/wayneblacktea/wbt.db` (via `$XDG_DATA_HOME`), which is persisted in `~/.config/wayneblacktea/config.yaml`. When running the server binary directly without a config or `SQLITE_PATH` env var, it falls back to `./wayneblacktea.db` in the current working directory. Set `SQLITE_PATH` (or `WBT_DB_PATH`) to an absolute path to pin the location.

**Permission errors**

The process must have read/write access to both the `.db` file and its parent directory. Check ownership: `ls -la wayneblacktea.db`.

**"no such table" errors**

The SQLite schema (`internal/storage/sqlite/schema.sql`) is applied automatically at boot via `//go:embed`. If you see "no such table", the `.db` file may have been created by an older binary. Delete it and let the server recreate it, or apply the schema manually:

```bash
sqlite3 wayneblacktea.db < internal/storage/sqlite/schema.sql
```

---

## Postgres / DATABASE_URL issues

**Connection string format**

```
DATABASE_URL=postgres://user:pass@host:5432/dbname?sslmode=require
```

Always include `?sslmode=require` for any network-accessible Postgres instance.

**SSL certificate verification (Aiven)**

For Aiven Postgres, set `PGSSLROOTCERT` to the path of the CA certificate downloaded from the Aiven console. When `APP_ENV=production` and `PGSSLROOTCERT` is unset, the server returns `ErrMissingPGSSLROOTCERT` at startup.

```bash
PGSSLROOTCERT=/path/to/ca.pem DATABASE_URL="postgres://..." go run ./cmd/server
```

**pgvector extension missing**

When using `STORAGE_BACKEND=postgres`, the `pgvector` extension must exist in the database:

```sql
CREATE EXTENSION IF NOT EXISTS vector;
```

---

## Claude Code MCP not connecting

**Standard setup — HTTP transport (recommended)**

`wbt setup` registers the MCP server automatically via:

```bash
claude mcp add wayneblacktea --transport http http://127.0.0.1:8420/mcp
```

If this step failed (e.g. `claude` was not in PATH during setup), re-run `wbt setup` after installing Claude Code, or register manually:

```bash
claude mcp add wayneblacktea --transport http http://127.0.0.1:8420/mcp
```

Verify the server is healthy and the MCP URL matches:

```bash
wbt status --format json   # shows the port the server is using
claude mcp list            # shows what Claude Code has registered
```

**Legacy stdio transport**

If you previously set up via `wbt init` and have a `.mcp.json` in your project directory, Claude Code will use stdio (`wbt mcp`). To migrate to the HTTP transport (which works from any directory without a `.mcp.json`), run `wbt setup` once, then remove the old stdio entry:

```bash
wbt setup
claude mcp remove wayneblacktea-stdio   # or whatever name the old entry had
```

To check HTTP connectivity directly:

```bash
curl -H "X-API-Key: $API_KEY" http://localhost:8420/health
```

---

## API key errors (401)

The server reads `API_KEY` from the environment at startup. All `/api/*` routes and `/mcp` require the header:

```
X-API-Key: <your-api-key>
```

Check that the value in your request matches the `API_KEY` env var exactly (no extra whitespace or quotes).

---

## Frontend / web UI not loading

The web UI is embedded into the server binary at build time via `//go:embed web/dist`. If `web/dist` does not exist when the binary is compiled, the embed step fails. Build the frontend before the Go binary:

```bash
cd web && npm ci && npm run build
cd .. && go build ./cmd/server
```

In CI, the `Build frontend` step in `.github/workflows/ci.yml` does this automatically.

---

## `task check` failing locally

**Missing `web/dist`**: build the frontend first (`cd web && npm ci && npm run build && cp -r dist ../cmd/server/web/dist`), then run `task check`.

**golangci-lint lock contention**: running multiple `task check` invocations in parallel can cause cache lock errors. Run one at a time, or clear the cache with `golangci-lint cache clean`.
