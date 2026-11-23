---
id: WBT3-700
title: "3.0 Data Model Overview"
status: frozen
order: 700
depends_on: [WBT3-004, WBT3-100]
owners: []
baseline_commit: 75ce929c301090f0d1e742591b311e90b712c5fe
last_reviewed: 2026-08-25
---

# 3.0 Data Model Overview

## New durable domains

Recommended minimal tables:

### Runtime
- `runtime_runs`
- `runtime_turns`
- `runtime_actions`
- `trace_spans`

### Presence
- `presence_sessions` — metadata only, if audit/recovery requires it

### Operators
- `operators`
- `operator_state`

### Event/World
- `events`
- `world_entities`
- `world_beliefs`

### Policy
- `action_approvals`

Do not persist every media frame.

## Existing domains reused

- `work_sessions`
- `work_session_evidence`
- `outcomes/evaluations`
- `pending_proposals`
- `ai_cost_ledger`
- `activity_log`

## No FK

Application layer validates references.

## Dual backend

Every user-visible durable domain must have Postgres and SQLite behavior parity unless an explicit documented exception meets existing project rules.
