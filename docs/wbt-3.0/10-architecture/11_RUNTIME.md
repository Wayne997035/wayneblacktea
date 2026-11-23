---
id: WBT3-110
title: "Personal Intelligence Runtime"
status: frozen
order: 110
depends_on: [WBT3-100]
owners: []
baseline_commit: 75ce929c301090f0d1e742591b311e90b712c5fe
last_reviewed: 2026-08-25
---

# Personal Intelligence Runtime

## Purpose

The Runtime is WBT's native reasoning/action host.

It is the architectural change that makes WBT usable directly without requiring Claude Code as the normal cognition host.

## Core types

```go
type Run struct {
    ID            uuid.UUID
    Trigger       Trigger
    Principal     Principal
    PresenceID    *uuid.UUID
    WorkSessionID *uuid.UUID
    OperatorID    *uuid.UUID
    Status        Status
    Budget        Budget
    ContextDigest string
    StartedAt     time.Time
    FinishedAt    *time.Time
}

type Budget struct {
    MaxTurns          int
    MaxDuration       time.Duration
    MaxModelCostUSD   float64
    MaxCapabilityCalls int
    MaxExternalWrites int
}
```

## Loop

```text
trigger
→ establish principal + budget
→ compile context
→ select model
→ generate
→ capability intent?
  ├─ no → final
  └─ yes
      → validate
      → policy
      → execute
      → observation/result
      → next turn
→ verify if action changed external state
→ final
→ trace/outcome proposal
```

## Required safeguards

- max turns
- max duration
- max cost
- max action count
- cancellation through `context.Context`
- identical-call/ping-pong loop guard
- provider deadline
- capability timeout
- explicit finish reason
- no raw chain-of-thought persistence

## Run types

- `interactive`
- `realtime_delegate`
- `operator`
- `event_analysis`
- `verification`
- `background_cognitive`

## Important distinction

Runtime does not automatically create a `work_session`.

A user asking a factual question should not create work lifecycle artifacts.
An engineering action can attach to the currently active `work_session`.

## Runtime result

Must contain:
- answer/result
- confidence/limitations when material
- capability actions summary
- verification summary
- durable proposals created
- trace id
