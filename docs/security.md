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
| `POST /api/session/handoff`, `POST /api/auto-handoff` | 5 req/s |
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

Binaries published in GitHub Releases are signed using Sigstore keyless signing (cosign).
Signing covers `checksums.txt`, and the checksum file covers every archive — so verifying
the one bundle transitively verifies all binaries. Each release includes a single
`checksums.txt.sigstore.json` bundle carrying both signature and certificate. To verify:

```bash
cosign verify-blob \
  --bundle wayneblacktea_checksums.txt.sigstore.json \
  --certificate-identity-regexp "https://github.com/Wayne997035/wayneblacktea/.github/workflows/release.yml@refs/tags/.*" \
  --certificate-oidc-issuer "https://token.actions.githubusercontent.com" \
  wayneblacktea_checksums.txt
```

---

## Hook binary logging

`cmd/wbt-hook` runs as a Claude Code `PostToolUse` hook and forwards a summary of each tool call to the server. Raw tool input is not stored: the hook caps stdin reads at 300 bytes and only forwards a content hash and tool name under normal operation. The server processes this through a classifier before any persistence. All hook binaries (`wbt-hook`, `wbt-context`, `wbt-doctor`) redirect `slog` output to a `0600` log file in the OS temporary directory at startup, preventing structured log messages from reaching the Claude Code terminal UI via stderr. Exception: `wbt-doctor` intentionally writes `ForgottenSignals` (stuck-task / overdue-review warnings) to stderr so they surface as operator-visible Stop-hook notices — this is by design and not a slog call.

---

## AI Mutation Gate

wayneblacktea distinguishes between an AI agent *proposing* a durable write and that write actually taking effect. Three related but distinct mechanisms enforce this, and they are not interchangeable:

- **`pending_proposals` gate** (goals, projects, and knowledge-derived concept cards): `propose_goal` and `propose_project` insert a row into `pending_proposals` with `status='pending'`; `add_knowledge` can auto-generate the same kind of pending concept proposal. The JSONB payload is not hidden — `list_pending_proposals` returns the full pending payload so an operator can inspect exactly what would be created before deciding anything. The entity is only materialized (the goal/project/concept row is created) when `confirm_proposal` or the batch `confirm_proposals` is called with `action='accept'`; `action='reject'` marks the proposal `rejected` and nothing is ever created (`internal/mcp/tools_proposal.go:69-118`, `:198-324`).
- **Behavior rule promotion** is a separate, narrower lifecycle: `propose_behavior_rule` inserts a `behavior_rules` row with `status='proposed'` (`internal/mcp/tools_behaviorrule.go:16`). It is not gated through `confirm_proposal` — a proposed rule is only promoted to `status='active'` when `apply_behavior_rules` records `outcome='success'` against it; it can also be moved to `deprecated` explicitly. Proposed and active rules are always visible via `list_behavior_rules` before promotion, and only `rejected`/`deprecated` rows are ever auto-deleted (see `docs/retention-policy.md`).
- **Skill extraction has no proposal gate today**: `extract_skill` (`internal/mcp/tools_skill.go:16`) persists the skill definition directly in the same call — there is no pending/confirm step for skills. `search_skills` / `list` output is the review surface for what an agent has recorded.

In all three cases the payload is queryable before or immediately after it lands — none of these paths perform a hidden mutation the operator cannot inspect.

---

## MCP Tool Description Trust Model

Tool descriptions (the `mcp.WithDescription(...)` strings passed to `mcp.NewTool` across `internal/mcp/tools_*.go`, and the server-wide instructions string registered at startup) are static text compiled into the binary. They are authored and code-reviewed as part of the source tree, so they are trusted — an attacker cannot alter what a tool claims to do without a code change.

By contrast, anything a tool call *returns* — knowledge item content, memory atom text, reflections, concept cards, skill descriptions, decision rationale — is agent- or user-generated data, some of which can originate from external sources (web content pulled in via `add_knowledge`, free-text reflections, agent-authored proposal payloads). Tool handlers marshal this data through a shared `jsonText` helper (`internal/mcp/server.go:493-500`), which JSON-encodes the value and returns it as tool-result text. The MCP client receives retrieved memory as a structured data payload to read, not as a new instruction stream: the server never re-interprets stored content as a system/tool-description string, and retrieved content is never substituted back into a prompt template the server itself constructs.

This distinction matters because an adversary who can get text into `knowledge_items`, `memory_atoms`, `reflections`, or a proposal payload (for example by prompting the agent to "remember" something, or via ingested web content) could attempt a classic memory/prompt-injection: crafting stored text that reads like an instruction, hoping a future agent session treats it as a directive rather than as data. The mitigation here is architectural, not a content filter — retrieved memory is always one JSON field among many in a tool result, never spliced into the tool-description or system-instruction strings the MCP client treats as authoritative. Consumers of tool output are expected to treat returned memory/knowledge text as data to reason about, not as a command channel.

---

## Retrieval Bounds

MCP tools that list or search memory/knowledge enforce a hard per-tool result cap at the handler layer, so no single call can force an unbounded scan or hand an unbounded amount of context back to the calling agent. The pattern is consistent across tools: read the caller-supplied `limit`, fall back to a small default when it is absent or non-positive, then clamp to a fixed maximum constant — some call sites also log a warning when the requested value is clamped down.

Representative examples (grep `maxListDecisionsLimit\|maxOutcomeLimit\|maxKnowledgeListLimit\|maxKnowledgeSearchLimit` to confirm current values):

- `internal/mcp/tools_decision.go:14,94-96` — `list_decisions` defaults to 20, clamps to `maxListDecisionsLimit = 100`, and logs `slog.Warn("list_decisions limit clamped", ...)` when the caller requests more.
- `internal/mcp/tools_outcome.go:51,250-251,282-283` — outcome-listing tools clamp requested limits to `maxOutcomeLimit = 100`.
- `internal/mcp/tools_knowledge.go:18,195-197,250-252` — `search_knowledge` clamps to `maxKnowledgeSearchLimit = 200` and `list_knowledge` clamps to `maxKnowledgeListLimit = 200`, the latter also logging a clamp warning.

Because the cap is applied in the handler before the query reaches the storage layer, no MCP tool call — regardless of what `limit` value an LLM constructs — can trigger an unbounded row scan or return an unbounded volume of data into the agent's context window.

---

## Supply chain

CI and release workflows pin GitHub Actions to full commit SHAs rather than mutable version tags. `govulncheck` runs on every push to `master` as part of `task check`.
