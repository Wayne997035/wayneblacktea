---
id: WBT3-720
title: "Runtime and Trace Schema"
status: frozen
order: 720
depends_on: [WBT3-110, WBT3-200, WBT3-700]
owners: []
baseline_commit: 75ce929c301090f0d1e742591b311e90b712c5fe
last_reviewed: 2026-08-25
---

# Runtime and Trace Schema

## `runtime_runs`

```text
id
workspace_id
trigger_type
trigger_ref
principal_kind
principal_id
presence_session_id
work_session_id
operator_id
status
budget_json
context_digest
started_at
finished_at
finish_reason
error_code
```

## `runtime_turns`

```text
id
run_id
sequence
model_provider
model_id
input_digest
output_digest
started_at
finished_at
finish_reason
usage_json
```

## `runtime_actions`

```text
id
run_id
turn_id
capability
capability_version
effect_class
risk_tier
status
idempotency_key
approval_id
external_id
verification_status
started_at
finished_at
error_code
```

## `trace_spans`

Structured causal span, not raw CoT.

## Retention

Keep enough for debugging/evals but apply TTL to high-volume telemetry.

Long-lived learning should be promoted into outcomes/knowledge, not achieved by retaining infinite raw Runtime traces.
