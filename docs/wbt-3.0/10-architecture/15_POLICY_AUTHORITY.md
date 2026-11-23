---
id: WBT3-150
title: "Policy, Authority and Approval"
status: frozen
order: 150
depends_on: [WBT3-140]
owners: []
baseline_commit: 75ce929c301090f0d1e742591b311e90b712c5fe
last_reviewed: 2026-08-25
---

# Policy, Authority and Approval

## Principal

Single-user does not mean single actor.

```go
type Principal struct {
    Kind string // human, presence, operator, mcp_agent, scheduler
    ID   string
}
```

## Decisions

- `ALLOW`
- `REQUIRE_CONFIRMATION`
- `DENY`

## Default matrix

| Effect | Interactive human request | Operator |
|---|---|---|
| READ | allow | allow within manifest |
| COMPUTE | allow | allow within budget |
| WRITE_REVERSIBLE | allow/confirm by target | policy |
| WRITE_EXTERNAL | confirm unless explicit scoped grant | confirm |
| DESTRUCTIVE | always confirm | deny by default |

## Approval token

Approval is a server-side object bound to:
- capability
- normalized arguments/target hash
- principal
- expiry
- single-use flag

Do not approve only by UI state.

## Failure semantics

For external/destructive actions:

```text
policy unavailable → DENY
approval lookup unavailable → DENY
target normalization failed → DENY
risk cannot be classified → DENY / T7
```

Read-only degraded behavior may remain available.

## Durable AI writes

Existing WBT proposal gate remains authoritative for inferred:
- tasks
- knowledge
- decisions
- rules
- skills
- memory governance changes

A Runtime Run does not bypass proposal semantics.
