---
id: WBT3-520
title: "World Surface"
status: frozen
order: 520
depends_on: [WBT3-180, WBT3-500]
owners: []
baseline_commit: 75ce929c301090f0d1e742591b311e90b712c5fe
last_reviewed: 2026-08-25
---

# World Surface

## Purpose

Show what WBT currently believes, and why.

## Sections

### Active context
- current work
- next event
- active incident
- current client/media state

### Entities
- projects/services/devices/objects
- current state
- freshness
- confidence
- source

### Observations
Recent meaningful observations with evidence class.

### Conflicts/stale
Beliefs that:
- expired
- conflict
- are inferred only
- need verification

## Physical object card

```text
Object #...
Candidate: Samsung 990 PRO 2TB
Confidence: 0.94
Observed via: camera
Evidence:
  label: MZ-V9P2T0
Freshness: current presence session
[Promote to known object]
```

## Critical UX

Clearly badge:
- OBSERVED
- INFERRED
- USER CONFIRMED
- STALE

Do not make all state look equally certain.
