---
id: WBT3-R02
title: "Baseline Evidence and Findings"
status: reference
order: 1001
depends_on: []
owners: []
baseline_commit: 75ce929c301090f0d1e742591b311e90b712c5fe
last_reviewed: 2026-08-25
---

# Baseline Evidence and Findings

## Finding F-01
WBT already has a provider-neutral JSON LLM chain.

Correction:
Do not create a separate replacement Model Gateway. Expand `internal/llm`.

## F-02
WBT already has deterministic context retrieval and ranking.

Correction:
Make `contextpack` a Runtime dependency, not a competing RAG system.

## F-03
WBT already has an eval harness.

Correction:
Extend `internal/evals`.

## F-04
WBT already has rich work session/evidence/outcome semantics.

Correction:
Create `runtime_run` only because its lifecycle/granularity is materially different. Link to Work Session.

## F-05
WBT already has 33 scheduler jobs.

Correction:
Scheduler remains timer source; migrate reasoning jobs gradually to Operator Runtime.

## F-06
WBT guard already has T0–T7 risk expertise.

Correction:
Reuse/extract taxonomy. Do not copy fail-open observability semantics into authorization.

## F-07
Current web architecture is management-dashboard first.

Correction:
New primary surfaces are collaboration-oriented; old views remain Explore.

## F-08
Browser screen sharing is not persistently permissioned.

Correction:
3.0 must describe session-scoped explicit sharing.

## F-09
Realtime provider capabilities differ.

Correction:
Perception normalizes frames/images; do not make native video a core assumption.

## F-10
The product role is an AI collaboration partner, not a relationship-oriented assistant.

Correction:
All language/personality specs use partner/crew/operator framing.
