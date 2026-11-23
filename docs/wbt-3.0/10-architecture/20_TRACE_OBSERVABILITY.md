---
id: WBT3-200
title: "Runtime Trace and Observability"
status: frozen
order: 200
depends_on: [WBT3-110, WBT3-140]
owners: []
baseline_commit: 75ce929c301090f0d1e742591b311e90b712c5fe
last_reviewed: 2026-08-25
---

# Runtime Trace and Observability

## Existing systems are not replaced

Keep:
- activity log
- watchdog
- discipline
- guard events
- AI cost ledger

New Runtime trace records causality.

## Trace tree

```text
Run
├─ Trigger
├─ ContextCompile
├─ ModelCall
├─ CapabilityIntent
├─ PolicyDecision
├─ CapabilityExecution
├─ Verification
├─ WorldUpdate
└─ Result
```

## Must answer

For any autonomous/proactive output:
- why did it start?
- what context was used?
- which model?
- what did the model request?
- what was authorized?
- what side effect occurred?
- what evidence verified it?
- how much did it cost?
- what state changed?

## Privacy

Do not persist private chain-of-thought.

Store:
- structured step type
- input/output digests
- sanitized summaries
- tool args after redaction where needed
- metadata
- evidence references

## Correlation

Trace IDs link:
- presence session
- work session
- operator
- event
- capability action
- outcome

No database FK constraints.
