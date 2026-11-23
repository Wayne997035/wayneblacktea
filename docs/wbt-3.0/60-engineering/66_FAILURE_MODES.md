---
id: WBT3-860
title: "Failure Modes"
status: frozen
order: 860
depends_on: [WBT3-100]
owners: []
baseline_commit: 75ce929c301090f0d1e742591b311e90b712c5fe
last_reviewed: 2026-08-25
---

# Failure Modes

| Failure | Concrete impact | Required control |
|---|---|---|
| runaway Runtime loop | cost/tool spam | turn/call/cost budgets + loop guard |
| duplicate webhook | duplicated action | dedup/idempotency |
| external write timeout ambiguity | duplicate email/merge | reconciliation/external id |
| realtime disconnect | broken voice session | reconnect/degraded text mode |
| ephemeral token leak | temporary provider misuse | short TTL/scope/rate limit |
| prompt injection in screen/email/web | action hijack | untrusted-data boundary + policy |
| VLM misidentification | wrong physical diagnosis | confidence/evidence + active perception |
| stale world belief | wrong current-state analysis | TTL/freshness |
| world inference promoted as truth | durable false context | belief class/proposal gate |
| noisy attention | user ignores WBT | correlation/cooldown/evals |
| operator stuck | invisible autonomy failure | lease/heartbeat/backoff |
| provider outage | WBT appears dead | fallback/degraded mode |
| media persistence bug | privacy incident | non-persistence default/tests |
| policy datastore unavailable | unsafe action | high-risk fail-closed |
| browser forged tool intent | unintended action request | server validation/policy/approval |
| scheduler/operator duplicate | duplicate cognition | one trigger ownership + dedup |
| work/runtime model confusion | bad lifecycle/learning | explicit IDs/relationship model |
| frontend state fiction | misleading user | UI only reflects actual runtime events |
