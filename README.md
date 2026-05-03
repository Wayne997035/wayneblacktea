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

```bash
go install github.com/Wayne997035/wayneblacktea/cmd/wbt@latest
wbt init    # picks SQLite or Postgres, writes .env + .mcp.json
```

Open Claude Code from the directory containing the generated `.mcp.json`; it will start `wbt mcp` automatically after you approve the project MCP server.

That's it. No Anthropic API key is required for core MCP memory features. `wbt serve` (optional) runs the dashboard's HTTP API alongside if you want the web UI.

## What you get

Once Claude Code is connected to `wbt mcp`, every MCP-capable agent reads and writes the same store:

| Context | What it tracks |
|---------|---------------|
| **GTD** | Goals → projects → tasks with importance, activity log |
| **Decisions** | Architectural choices with rationale + alternatives, queryable by repo |
| **Knowledge** | Articles, TILs, bookmarks, Zettelkasten notes — full-text + pgvector semantic search |
| **Learning** | Spaced-repetition concept cards on an FSRS schedule, auto-proposed from new knowledge |
| **Sessions** | Cross-session handoff notes — "what to continue next time" |
| **Proposals** | Agent-originated entities awaiting your confirmation before they materialise |
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

## What this *isn't*

- **Not a team product.** One person, many agents. No RBAC, no shared workspace, no Notion-clone collaboration.
- **Not a hosted service.** Self-host on your own machine. Workspace scoping is for personal data isolation, not multi-tenancy.
- **Not a stable API.** Solo project, irregular releases, breaking changes happen, dashboard rough edges remain.
- **Not a chatbot with memory.** The schema is the memory. Conversation history is irrelevant.

---

Licensed under [MIT](./LICENSE). Architecture deep-dive in [`docs/architecture.md`](./docs/architecture.md).
