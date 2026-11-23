---
id: WBT3-910
title: "Release Gates"
status: frozen
order: 910
depends_on: [WBT3-900]
owners: []
baseline_commit: 75ce929c301090f0d1e742591b311e90b712c5fe
last_reviewed: 2026-08-25
---

# Release Gates

WBT 3.0 cannot be called complete until all required gates pass.

## G1 — Direct collaboration
User can open WBT and converse without Claude Code.

## G2 — Voice
Natural realtime voice with interruption and fallback.

## G3 — Physical object
Camera/object workflow can identify, expose uncertainty, request a better view and use context/tools.

## G4 — Screen analysis
User can explicitly share screen and obtain context-aware analysis.

## G5 — Action
At least one reversible and one external capability run through Gateway/Policy/Trace.

## G6 — World
Current state model differentiates observed/inferred/stale and is inspectable.

## G7 — Proactivity
An event can be correlated, scored and produce a useful notification/proposal without direct user prompt.

## G8 — Operator
Persistent operator survives multiple ticks/runs with health/budget controls.

## G9 — Continuity
New provider/presence session can resolve recent work from WBT durable state.

## G10 — Governance
AI-originated durable knowledge/rules/skills still require existing proposal/governance paths.

## G11 — Security
No provider master key in browser; high-risk policy fail closed.

## G12 — Quality
`task check` passes plus 3.0 deterministic eval suite.
