# Security Model

This document is for operators and security reviewers. It covers authentication, transport security, and the scope of the security model for self-hosted instances.

---

## Authentication

wayneblacktea is a single-tenant personal OS. One `API_KEY` environment variable protects all `/api/*` routes and the `/mcp` endpoint. There is no RBAC and no multi-user support — the design assumption is a single owner.

Separately, the web UI obtains a session cookie (`wbt_session`) by calling `POST /api/session` with the correct `X-API-Key` header. The cookie is `httpOnly` and `SameSite=Strict`, which prevents JavaScript access and cross-site submission.

---

## CORS

Wildcard origins (`ALLOWED_ORIGINS=*`) are unconditionally rejected at startup. This is because the server sets `AllowCredentials: true` to allow the browser to send the `wbt_session` cookie in cross-origin requests — browsers refuse to honour credentials with a wildcard `Access-Control-Allow-Origin`.

`ALLOWED_ORIGINS` must be a comma-separated list of explicit origins in production:

```
ALLOWED_ORIGINS=https://your-domain.example.com
```

When `APP_ENV` is not set to `production` and `ALLOWED_ORIGINS` is empty, the server defaults to `http://localhost:<PORT>,http://127.0.0.1:<PORT>` and logs a warning. This default is intentional for local development only.

---

## Rate limiting

Per-IP rate limiting is applied to write-heavy and high-frequency endpoints. IP is extracted from the `X-Real-IP` header set by Railway's edge proxy. The `X-Forwarded-For` header is not trusted, as clients can prepend arbitrary values to spoof the leftmost hop and bypass per-IP limiters.

| Endpoint group | Limit |
|---|---|
| `POST /api/knowledge`, `PATCH /api/knowledge/:id`, `GET /api/knowledge/search` | 20 req/s |
| `POST /api/session/handoff`, `POST /api/auto-handoff` | 5 req/min |
| `GET /api/proposals`, `POST /api/proposals/*` | 10 req/s |
| `GET /api/search` | 20 req/s |
| `GET /api/dashboard/*` | 30 req/s |

---

## SQLite security notes

When running with `STORAGE_BACKEND=sqlite`, the database is a local file. There is no network exposure. The file has no encryption at rest; it is suitable for single-user local development on a trusted machine. For production use, switch to Postgres with TLS (`?sslmode=require`).

---

## Postgres TLS

Always include `?sslmode=require` in `DATABASE_URL` for any network-accessible Postgres instance. For providers that use a custom certificate authority (such as Aiven), set `PGSSLROOTCERT` to the path of the CA certificate bundle. When `APP_ENV=production` and `PGSSLROOTCERT` is unset, the server refuses to start.

---

## Release artifact signing

Binaries published in GitHub Releases are signed using Sigstore keyless signing (cosign). Each release includes `.pem` and `.bundle` files alongside the binary archives. To verify:

```bash
cosign verify-blob \
  --certificate <binary>.pem \
  --bundle <binary>.bundle \
  <binary>
```

---

## Hook binary logging

`cmd/wbt-hook` runs as a Claude Code `PostToolUse` hook and forwards a summary of each tool call to the server. Raw tool input is not stored: the hook caps stdin reads at 300 bytes and only forwards a content hash and tool name under normal operation. The server processes this through a classifier before any persistence. Log files written by hook binaries are placed in the OS temporary directory with mode `0600`.

---

## Supply chain

CI and release workflows pin GitHub Actions to full commit SHAs rather than mutable version tags. `govulncheck` runs on every push to `master` as part of `task check`.
