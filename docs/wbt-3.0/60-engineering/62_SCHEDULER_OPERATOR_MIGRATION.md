---
id: WBT3-820
title: "Scheduler to Operator Migration"
status: frozen
order: 820
depends_on: [WBT3-160, WBT3-810]
owners: []
baseline_commit: 75ce929c301090f0d1e742591b311e90b712c5fe
last_reviewed: 2026-08-25
---

# Scheduler to Operator Migration

## Do not rewrite 33 jobs

Classify each job.

### Class A — maintenance
Examples: prune jobs.
Remain in scheduler.

### Class B — deterministic detection
Can remain scheduler or emit Event.

Preferred 3.0 pattern:
`scheduler → event → attention/operator`

### Class C — reasoning/cognitive
Move reasoning into Runtime/Operator.

## Migration pattern

Before:
```text
cron callback
→ query stores
→ call LLM
→ create proposal
```

After:
```text
cron callback
→ publish typed trigger event
→ Operator
→ Runtime
→ context/model/capability/proposal
```

## Benefit

One reasoning path for:
- Discord
- Presence
- scheduled work
- event-driven work

instead of four AI loops.

## Completion criterion

No new scheduled AI reasoning may be written directly into a scheduler callback after the Operator Runtime is available, unless an ADR justifies it.
