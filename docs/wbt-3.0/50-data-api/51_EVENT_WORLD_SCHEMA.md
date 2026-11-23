---
id: WBT3-710
title: "Event and World Schema"
status: frozen
order: 710
depends_on: [WBT3-170, WBT3-180, WBT3-700]
owners: []
baseline_commit: 75ce929c301090f0d1e742591b311e90b712c5fe
last_reviewed: 2026-08-25
---

# Event and World Schema

## `events`

Suggested columns:

```text
id
workspace_id
type
source
subject
observed_at
received_at
trust
sensitivity
priority_hint
correlation_id
dedup_key
payload_json
retention_class
created_at
```

Indexes:
- type + observed_at
- source + dedup_key
- correlation_id
- workspace + observed_at

## `world_entities`

```text
id
workspace_id
kind
canonical_name
external_refs_json
status
created_at
updated_at
```

## `world_beliefs`

```text
id
workspace_id
entity_id
predicate
value_json
belief_kind
confidence
source
evidence_ref
observed_at
valid_until
superseded_at
created_at
updated_at
```

No FK.

## Current belief query

Primary read path selects non-superseded belief with:
- valid_until null or future
- highest source precedence/confidence
- latest observed_at

Do not delete contradictory belief evidence immediately.

## Retention

Ephemeral camera-derived object observations should normally not enter `world_entities`/`world_beliefs` until promotion criteria are met.
