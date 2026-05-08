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
  A personal-OS MCP server for AI agents — your goals, decisions, knowledge,
  and learning live in one shared brain so the AI you work with already
  knows your context instead of asking you to re-explain it every conversation.
</p>

---

## Install

Single binary, interactive wizard, SQLite-by-default — no infra to provision.

**MCP stdio (simplest — no server process needed):**

```bash
go install github.com/Wayne997035/wayneblacktea/cmd/wbt@latest
wbt init    # wizard: picks SQLite or Postgres, writes .env + .mcp.json
```

Open Claude Code from the directory containing the generated `.mcp.json`; it will start `wbt mcp` automatically after you approve the project MCP server.

**With dashboard + HTTP MCP transport:**

```bash
# Also build the server binary
go build -o "$(go env GOPATH)/bin/wayneblacktea-server" ./cmd/server

wbt serve   # loads config → starts server → opens http://localhost:8080
```

Then add the HTTP transport to Claude Code once the server is running:

```bash
claude mcp add --transport http wayneblacktea http://localhost:8080/mcp
```

No Anthropic API key is required for core MCP memory features. See [`docs/install.md`](./docs/install.md) for Postgres, Docker, and Railway options.

## 5-minute onboarding

After `wbt init`, open Claude Code from the directory containing `.mcp.json`, then try:

```
# Check what's already remembered from past sessions
> get_today_context

# Log a decision (stored permanently, queryable by repo)
> log_decision "chose SQLite over Postgres for this project because..."

# Add a task with context
> add_task "implement login flow" --project my-project

# Confirm a multi-step plan atomically
> confirm_plan {phases: [...], decisions: [...]}

# Add a knowledge note (searchable by keyword + semantic vector)
> add_knowledge {title: "JWT expiry best practices", content: "..."}

# Store a deferred idea that's not ready to be a task yet
> add_vision_item {title: "multi-agent coordination protocol", why_blocked: "needs E1 provider interface first"}
```

Full tool reference: [`docs/mcp-tools.md`](./docs/mcp-tools.md).

## Architecture

```mermaid
flowchart TD
    CC["Claude Code\n(wbt mcp — stdio)"]
    CURL["HTTP clients\n(dashboard / curl)"]
    HTTPMCP["HTTP MCP transport\n(/mcp)"]

    CC   -->|MCP stdio|       SRV
    CURL -->|REST /api/star|  SRV
    HTTPMCP -->|MCP over HTTP| SRV

    SRV["wayneblacktea-server\n(Echo HTTP + mcp-go)"]

    SRV --> STORES["Store interfaces\ngtd · decision · knowledge · session\nproposal · vision · learning · workspace"]

    STORES --> SQLITE["SQLite\n(local dev, zero infra)"]
    STORES --> PG["Postgres + pgvector\n(Aiven / Railway)"]

    SRV --> AI["AI providers\nAnthropic · Gemini embeddings\nGroq (Discord bot)"]
    SRV --> SCHED["Scheduler\nSaturday reflection\nauto-consolidation · decay prune"]
    SRV --> DISCORD["Discord bot"]
```

One process serves MCP stdio, HTTP REST, and HTTP MCP — no separate components to run.

## What you get

Once Claude Code is connected to `wbt mcp`, every MCP-capable agent reads and writes the same store:

| Context | What it tracks |
|---------|---------------|
| **GTD** | Goals → projects → tasks with importance, activity log |
| **Decisions** | Architectural choices with rationale + alternatives, queryable by repo |
| **Knowledge** | Articles, TILs, bookmarks, notes — full-text + pgvector semantic search; Markdown headings fan-out into searchable child nodes |
| **Learning** | Spaced-repetition concept cards on an FSRS schedule, auto-proposed from new knowledge |
| **Sessions** | Cross-session handoff notes — "what to continue next time" |
| **Proposals** | Agent-originated entities awaiting your confirmation before they materialise |
| **Vision** | Deferred ideas that aren't ready to be tasks yet — `open → discussing → maturing → promoted → dismissed` |
| **Workspace** | Tracked Git repos with status, known issues, next planned step |

## Auto-memory (no nagging required)

The agent doesn't need to remember to save things. The server captures them automatically:

- **MCP middleware classifier** — every significant tool call (`complete_task`, `confirm_proposal`, `upsert_project_arch`, `update_project_status`, `resolve_handoff`, `sync_repo`) is async-classified by Haiku; implicit decisions get auto-logged, follow-up tasks get auto-created. Rate-capped at 60/min, deduped, prompt-injection-bounded.
- **Stop hook** (`wbt-doctor`) — when a Claude Code session ends, the transcript is compressed to a ≤500-char summary and stored as both `session_handoffs.summary_text` and a searchable `knowledge_items` row.
- **SessionStart hook** (`wbt-context`) — the next session opens with the previous handoff, recent decisions, and due reviews already injected as context.
- **Saturday reflection cron** — weekly batch reads 7 days of activity + decisions and proposes 3-5 retrospective knowledge entries (gated through `pending_proposals` for your confirmation).
- **Auto-consolidation** — clusters of ≥5 same-actor activities in the last 30 days get merged into a single proposed knowledge entry.

## Design

**Structure beats prompts.** Encode what you want the AI to remember as an explicit schema. No drift between agents, no "I think you mentioned…" — it's just data.

**You stay in control.** Agents propose, you confirm. The friction is the point — a system that decides for you eventually decides everything.

**Make forgetting visible.** The server tracks every tool call and surfaces forgotten work — stuck `in_progress` tasks, piled-up proposals, decisions logged without a session-start recall.

**Workflow tools, not raw CRUD.** The agent surface exposes verbs like "get today's context", "confirm this plan", "log this decision" — rules live in the tool layer, not scattered across each client's prompt.

## vs similar tools

| | wayneblacktea | claude-mem | Hermes Agent Memory |
|---|---|---|---|
| **Scope** | Full personal OS (GTD + decisions + knowledge + learning + vision) | Conversation memory only | Conversation + entity memory |
| **Storage** | SQLite (local) or Postgres+pgvector (cloud) | Managed cloud | Managed cloud |
| **Control** | Self-hosted, you own the data | Third-party | Third-party |
| **Proposals gate** | Agent proposes → you confirm | None | None |
| **Spaced repetition** | FSRS learning cards | None | None |
| **Dashboard** | Web UI + Discord bot | None | None |
| **Tradeoff** | More setup, more surface area | Zero setup | Zero setup |

## Verifying release binaries

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

The `.sig` and `.pem` files are attached to each GitHub Release alongside the binaries.

## What this *isn't*

- **Not a team product.** One person, many agents. No RBAC, no shared workspace, no Notion-clone collaboration.
- **Not a hosted service.** Self-host on your own machine. Workspace scoping is for personal data isolation, not multi-tenancy.
- **Not a stable API.** Solo project, irregular releases, breaking changes happen, dashboard rough edges remain.
- **Not a chatbot with memory.** The schema is the memory. Conversation history is irrelevant.

---

Licensed under [MIT](./LICENSE). Architecture deep-dive in [`docs/architecture.md`](./docs/architecture.md).
