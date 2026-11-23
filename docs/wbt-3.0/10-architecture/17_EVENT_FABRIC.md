---
id: WBT3-170
title: "Event Fabric"
status: frozen
order: 170
depends_on: [WBT3-100]
owners: []
baseline_commit: 75ce929c301090f0d1e742591b311e90b712c5fe
last_reviewed: 2026-08-25
---

# Event Fabric

## Purpose

Normalize "something happened" across WBT, GitHub, calendar, user media and future sources.

## Event envelope

```go
type Event struct {
    ID            uuid.UUID
    Type          string
    Source        string
    Subject       string
    ObservedAt    time.Time
    ReceivedAt    time.Time
    Trust         TrustLevel
    Sensitivity   Sensitivity
    PriorityHint  int
    WorkspaceID   *uuid.UUID
    CorrelationID string
    DedupKey      string
    Payload       json.RawMessage
}
```

## Naming

Examples:
- `wbt.task.stale`
- `wbt.decision.outcome_missing`
- `github.workflow.failed`
- `calendar.event.upcoming`
- `presence.user.command`
- `perception.object.observed`
- `world.entity.changed`

## Pipeline

```text
source adapter
→ normalize
→ validate
→ dedup
→ correlation
→ world update (if applicable)
→ attention
→ operator/runtime trigger (if applicable)
→ trace
```

## Trust

At minimum:
- `trusted_internal`
- `authenticated_external`
- `untrusted_external`
- `model_derived`

Event data never becomes instruction authority merely because it entered the bus.

## Delivery semantics

MVP can be in-process with durable important events.
Do not introduce Kafka/NATS for a single-user system without measured need.

## Retention

Not every event is durable knowledge.

Use retention classes and promote only meaningful outcomes.
