---
id: WBT3-210
title: "Work Session Integration"
status: frozen
order: 210
depends_on: [WBT3-110, WBT3-200]
owners: []
baseline_commit: 75ce929c301090f0d1e742591b311e90b712c5fe
last_reviewed: 2026-08-25
---

# Work Session Integration

## Why Runtime Run cannot replace Work Session

Current `work_session` represents the user's durable unit of project work and already owns verification evidence/outcome linkage.

A runtime run is much smaller.

Example:

```text
Work Session: "Implement WBT Runtime MVP"
  ├─ Run 1: inspect architecture
  ├─ Run 2: answer voice question
  ├─ Run 3: generate patch
  ├─ Run 4: inspect test failure
  └─ Run 5: verification analysis
```

## Link rules

Runtime detects an active relevant work session through context.
It may attach `work_session_id`.

Capability evidence that materially proves work may be summarized into existing `work_session_evidence`.

Do not copy every tool call into work evidence.

## Outcome

When a meaningful work session finishes:
- existing `finish_work`
- verification
- outcome/evaluation
- skill/rule learning

remain the durable learning loop.

Runtime trace adds richer execution evidence, not a replacement domain.
