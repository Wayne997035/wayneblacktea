---
id: WBT3-180
title: "World Model"
status: frozen
order: 180
depends_on: [WBT3-170]
owners: []
baseline_commit: 75ce929c301090f0d1e742591b311e90b712c5fe
last_reviewed: 2026-08-25
---

# World Model

## Purpose

Represent WBT's current, evidence-backed belief about relevant entities and state.

## Inspiration

Use the proven separation seen in systems such as Home Assistant:

```text
event bus
+ entity registry
+ current state
+ service/action registry
```

WBT adds uncertainty/provenance semantics.

## Entity

Examples:
- user
- project
- repo
- service
- device
- application
- incident
- physical object
- location
- person/contact

## Belief

Use property-level beliefs rather than one opaque JSON snapshot.

```go
type Belief struct {
    EntityID       uuid.UUID
    Predicate      string
    Value          json.RawMessage
    BeliefKind     string // observed, user_asserted, derived, inferred
    Confidence     float64
    ObservedAt     time.Time
    ValidUntil     *time.Time
    EvidenceRef    *string
}
```

## Rules

- observation ≠ inference
- current belief ≠ long-term knowledge
- stale real-world state must expire
- conflicting observations stay visible until resolved
- user correction wins over weak inference
- high-confidence device/API telemetry can outrank visual inference for that property

## Physical object lifetime

Objects first seen in camera are ephemeral.

Promotion to durable `world_entity` happens only when:
- the user names/saves it
- it maps to a known durable entity
- an integration provides a stable identity
- policy explicitly allows automatic registration

## No automatic memory pollution

World state may later produce:
- outcome
- knowledge proposal
- memory atom proposal

but only through existing governance.
