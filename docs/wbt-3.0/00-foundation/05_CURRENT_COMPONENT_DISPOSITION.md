---
id: WBT3-005
title: "Current Component Disposition"
status: frozen
order: 15
depends_on: [WBT3-003]
owners: []
baseline_commit: 75ce929c301090f0d1e742591b311e90b712c5fe
last_reviewed: 2026-08-25
---

# Current Component Disposition

| Existing component | 3.0 disposition | Notes |
|---|---|---|
| `internal/contextpack` | EVOLVE | becomes Context Compiler input for Runtime |
| `internal/llm` | EVOLVE | add general/multimodal/realtime contracts; preserve JSONClient |
| `internal/ai` | SHRINK/FOCUS | feature-specific analyzers remain; provider ownership moves toward `internal/llm` |
| `internal/evals` | EVOLVE | add runtime/world/attention/perception graders |
| `internal/worksession` | KEEP | work lifecycle; Runtime links to it |
| `internal/scheduler` | KEEP + EXTRACT REASONING | time triggers stay; reasoning migrates to operator/runtime |
| `internal/guard` | KEEP + SHARE RISK | Claude Code hook guard remains; shared risk taxonomy extracted gradually |
| `internal/mcp` | KEEP | external agent client surface; not WBT's native brain |
| `internal/proposal` | KEEP | durable AI-originated writes stay gated |
| `cmd/server` | REFACTOR ASSEMBLY | avoid adding all 3.0 wiring directly to already-large main |
| `web/` | EVOLVE | new IA/surfaces; legacy pages preserved under Explore |
| Postgres/SQLite | KEEP | dual-backend parity |
| `activity_log` | KEEP | not a Runtime trace substitute |
| `ai_cost_ledger` | KEEP | Runtime model usage should feed the same cost system |
| `watchdog/discipline` | KEEP | complementary observability, not Runtime trace replacement |

## New bounded contexts / packages

Recommended new ownership:

```text
internal/runtime
internal/capability
internal/policy
internal/operator
internal/event
internal/world
internal/attention
internal/presence
internal/perception
internal/trace
```

Some may start as packages without storage tables. Do not create tables just to match a diagram.
