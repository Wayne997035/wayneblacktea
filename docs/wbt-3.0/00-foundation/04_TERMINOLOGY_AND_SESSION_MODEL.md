---
id: WBT3-004
title: "Terminology and Session Model"
status: frozen
order: 14
depends_on: [WBT3-003]
owners: []
baseline_commit: 75ce929c301090f0d1e742591b311e90b712c5fe
last_reviewed: 2026-08-25
---

# Terminology and Session Model

3.0 introduces several execution concepts. They MUST not be called simply "session" in code or docs.

## `session_handoff`
Existing WBT continuity note.

Purpose: carry unfinished context between work/conversation periods.

## `work_session`
Existing durable unit of human/engineering work.

Purpose:
- project/task linkage
- implementation activity
- verification evidence
- outcome

Lifetime: minutes to hours/days.

## `presence_session`
New live user interface/media session.

Purpose:
- text/voice interaction
- realtime provider connection
- camera/screen media
- client capability state
- ephemeral credentials

Lifetime: seconds to hours.

Not authoritative memory.

## `runtime_run`
New atomic reasoning/action execution.

Triggered by:
- user intent
- operator
- event
- explicit deep analysis delegation

Contains:
- ContextPack reference/digest
- model calls
- capability intents/results
- trace
- result

Lifetime: seconds/minutes.

## `operator`
Persistent definition + state that can create runtime runs over time.

## `event`
A normalized occurrence that may trigger relevance evaluation.

## `observation`
Evidence about the world.

## `world_belief`
Current best-known state/fact with source, confidence and validity window.

## Required relationships

```text
presence_session 1 ── N runtime_run
work_session     1 ── N runtime_run
operator         1 ── N runtime_run

event ──> runtime_run (optional)
runtime_run ──> outcome / work evidence (optional)
observation ──> world_belief
```

No DB foreign keys. Application-level integrity only.
