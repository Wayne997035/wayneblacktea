# Troubleshooting

Common setup failures for new instances.

---

## `wbt serve` / server won't start

**`API_KEY not set`**

The server exits at startup if `API_KEY` is empty. Set it in `.env` or export it before running. If you used `wbt init`, the key was written to `.env` automatically.

**`ALLOWED_ORIGINS` panic**

`CORSMiddleware` panics at startup when `ALLOWED_ORIGINS` is `"*"` or empty after resolution. In local dev the server defaults to `http://localhost:<PORT>,http://127.0.0.1:<PORT>` — this only works when `APP_ENV` is not set to `production`. In production you must set `ALLOWED_ORIGINS` explicitly:

```
ALLOWED_ORIGINS=https://your-domain.example.com
```

**Database connection refused**

`STORAGE_BACKEND` controls which backend is used. When unset it defaults to `sqlite` — no running database required. Set `STORAGE_BACKEND=postgres` only when a Postgres server is reachable and `DATABASE_URL` is set.

**Port already in use**

The server defaults to port `8420`. Override with `PORT=<number>` in `.env`.

---

## SQLite backend issues

**Where the file is created**

When `SQLITE_PATH` is unset, the server creates `./wayneblacktea.db` in the current working directory. Set `SQLITE_PATH` to an absolute path to pin the location.

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

**`.mcp.json` location and format**

Run `wbt init` to generate `.mcp.json` in the current directory. The generated file uses `wbt mcp` as the command (stdio transport):

```json
{
  "mcpServers": {
    "wayneblacktea": {
      "command": "wbt",
      "args": ["mcp"],
      "env": { "STORAGE_BACKEND": "sqlite" }
    }
  }
}
```

Open Claude Code from the directory containing `.mcp.json`.

**HTTP MCP transport**

The server also exposes an HTTP MCP endpoint at `/mcp`. Claude Code must be configured with transport `http` (not `sse`) and must send `X-API-Key` on every request. Verify connectivity:

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
