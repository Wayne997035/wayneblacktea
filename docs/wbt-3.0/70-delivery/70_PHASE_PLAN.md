---
id: WBT3-900
title: "3.0 Phase Plan"
status: frozen
order: 900
depends_on: [WBT3-100]
owners: []
baseline_commit: 75ce929c301090f0d1e742591b311e90b712c5fe
last_reviewed: 2026-08-25
---

# 3.0 Phase Plan

All phases belong to WBT 3.0 program; 3.0 release is cut only after release gates.

## Phase 0 — Architecture preparation

- freeze spec
- extract/share risk taxonomy
- define Runtime contracts
- define package assembly boundaries
- update current docs to reference 3.0 program
- no behavior change

## Phase 1 — Native Runtime + text Presence

- runtime runs/turns/trace
- ContextPack integration
- expanded `internal/llm`
- read-only capability adapter
- Web Presence text conversation
- no external write

## Phase 2 — Capability + Policy

- capability registry
- risk/effect
- approvals
- idempotency/verification
- WBT reversible writes via proposal gate

## Phase 3 — Web 3.0 shell

- new IA
- Presence Core
- Operations trace
- Control
- legacy Explore
- meaningful motion

## Phase 4 — Realtime Voice

- provider bootstrap/ephemeral credentials
- realtime session
- interrupt
- deep-analysis delegation
- transcript/context continuity

## Phase 5 — Visual Perception

- image upload
- camera frames
- screen share
- VLM/OCR adapter
- active perception
- physical-object acceptance demo

## Phase 6 — Event + World + Attention

- event fabric
- world entities/beliefs
- attention scoring
- World UI
- proactive server notifications

## Phase 7 — Persistent Operators

- manifest/state
- scheduler trigger integration
- event-triggered runs
- health/backoff
- migration of selected cognitive jobs

## Phase 8 — Closed-loop hardening

- verification
- outcome integration
- eval expansion
- privacy/retention
- SLO hardening
- release demos

## Optional packaging track

WBT Desktop Client using Go + React/TS may run in parallel after Web Presence is stable.
It is not allowed to block the core architecture.
