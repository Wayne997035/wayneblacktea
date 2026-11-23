---
id: WBT3-760
title: "Retention and Privacy"
status: frozen
order: 760
depends_on: [WBT3-200, WBT3-320, WBT3-700]
owners: []
baseline_commit: 75ce929c301090f0d1e742591b311e90b712c5fe
last_reviewed: 2026-08-25
---

# Retention and Privacy

## Default media retention

| Data | Default |
|---|---|
| raw microphone audio | not persisted |
| raw camera frames | not persisted |
| raw screen frames | not persisted |
| realtime provider media buffer | provider/session transient |
| transcript | configurable; default bounded |
| perception metadata | short-lived if needed |
| promoted artifact | explicit user action/policy |
| Runtime trace metadata | retained by Runtime policy |
| durable knowledge/outcome | existing governance |

## Media indicators

User must always know:
- microphone active
- camera active
- screen active
- cloud provider receiving media

## Sensitivity

Context/world/event items carry sensitivity labels used by routing policy.

## Redaction

Existing WBT redaction principles apply before:
- trace storage
- evidence excerpt persistence
- cloud transmission where deterministic secret detection is feasible

## Right to forget

Deleting a promoted object/trace must not require retaining its raw media because raw media was never default durable state.
