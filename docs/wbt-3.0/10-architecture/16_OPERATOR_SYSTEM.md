---
id: WBT3-160
title: "Persistent Operator System"
status: frozen
order: 160
depends_on: [WBT3-110, WBT3-140, WBT3-150]
owners: []
baseline_commit: 75ce929c301090f0d1e742591b311e90b712c5fe
last_reviewed: 2026-08-25
---

# Persistent Operator System

## Operator

A persistent mission definition that wakes due to time/event triggers and starts bounded Runtime runs.

## Manifest

```yaml
id: engineering-watch
objective: >
  Track meaningful engineering failures and prepare evidence-backed remediation.
triggers:
  - event: github.workflow.failed
  - schedule: "0 */2 * * *"
context:
  repo_scope: active
capabilities:
  allow:
    - github.read
    - wbt.read
    - wbt.propose
budget:
  max_turns: 8
  max_duration_seconds: 90
  max_cost_usd: 0.15
attention:
  minimum_action: QUEUE
```

## Lifecycle

`REGISTERED → ACTIVE → RUNNING → ACTIVE`

Additional:
- PAUSED
- DEGRADED
- DISABLED

## State

Store only structured operational state:
- cursor
- last event id
- last success/failure
- failure streak
- checkpoint
- backoff until

Do not use a conversational response blob as the authoritative operator state.

## Integration with scheduler

Scheduler:
> emit a time trigger

Operator:
> determine whether/how to run

Existing cognitive jobs should migrate gradually:
- deterministic maintenance/pruning stays scheduler code
- reasoning-oriented recurring jobs become operator-triggered Runtime work
- no flag-day rewrite of all 33 jobs

## Required controls

- singleton/concurrency
- lease/heartbeat
- retry/backoff
- pause/resume
- dry run
- budget
- capability scope
- health
- version
