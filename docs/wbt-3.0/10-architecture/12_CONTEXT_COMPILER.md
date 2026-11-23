---
id: WBT3-120
title: "Context Compiler Evolution"
status: frozen
order: 120
depends_on: [WBT3-110]
owners: []
baseline_commit: 75ce929c301090f0d1e742591b311e90b712c5fe
last_reviewed: 2026-08-25
---

# Context Compiler Evolution

## Existing base

`internal/contextpack` is retained.

3.0 changes its role from an MCP convenience tool into a required Runtime dependency.

## New inputs

Existing:
- objective
- repo
- project
- task
- branch/files
- type filters
- budget

Add:
- trigger event
- presence session summary
- world entity ids
- active operator objective
- required capability
- sensitivity policy
- recency horizon

## New source classes

Existing WBT sources remain primary.

3.0 adds:
- current `world_beliefs`
- recent correlated events
- recent runtime outcomes
- operator checkpoint/state

## Deterministic-first rule

LLM ranking may refine candidate order, but:
- hard scope
- stale exclusion
- active rule selection
- per-type caps
- token/character budgets
- sensitive routing labels

must remain reproducible.

## Provenance

Every included item must expose:
- source type
- source id
- observed/created/updated time
- current/stale status
- reason for inclusion

## Context layers

```text
L0 stable identity / hard policy
L1 current objective + work state
L2 directly relevant decisions/rules/skills
L3 world state / current observations
L4 supporting knowledge/outcomes
L5 optional broad retrieval
```

Lower number = harder to evict.

## Realtime use

Do not dump a 12k-character pack into every voice turn.

Presence Session gets:
- compact stable profile
- short current context summary

Deep analysis uses a Runtime Run with a full ContextPack.
