---
id: WBT3-800
title: "Package Ownership"
status: frozen
order: 800
depends_on: [WBT3-005, WBT3-100]
owners: []
baseline_commit: 75ce929c301090f0d1e742591b311e90b712c5fe
last_reviewed: 2026-08-25
---

# Package Ownership

## Recommended Go packages

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

## Existing package evolution

```text
internal/contextpack   retain/evolve
internal/llm           retain/evolve
internal/evals         retain/evolve
internal/worksession   retain
internal/scheduler     retain, narrow reasoning ownership
internal/guard         retain; share risk taxonomy
internal/mcp           retain client surface
```

## Assembly

`cmd/server/main.go` is already large.

3.0 SHOULD introduce bounded assembly functions/package(s), e.g.:

```text
internal/app/runtime_wiring.go
internal/app/presence_wiring.go
internal/app/operator_wiring.go
```

without turning `internal/app` into a service locator.

Constructors should receive narrow interfaces.

## Import direction

Domain/runtime packages do not import Echo/React/provider SDK types.

Adapters may import domain contracts.
