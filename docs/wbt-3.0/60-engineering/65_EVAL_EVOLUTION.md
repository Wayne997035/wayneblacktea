---
id: WBT3-850
title: "Eval Evolution"
status: frozen
order: 850
depends_on: [WBT3-003, WBT3-810]
owners: []
baseline_commit: 75ce929c301090f0d1e742591b311e90b712c5fe
last_reviewed: 2026-08-25
---

# Eval Evolution

## Existing package

Use `internal/evals`.

## New deterministic suites

### Runtime
- terminates at max turns
- cancellation
- duplicate tool call guard
- budget enforcement
- no provider call when denied precondition

### Capability/Policy
- deny unknown T7
- external write confirmation
- idempotency
- no blind retry

### Attention
Synthetic event streams:
- repeated noise collapses
- urgent production event interrupts
- irrelevant branch stays quiet

### World Model
- TTL expiry
- stronger evidence supersedes weaker
- contradiction surfaced
- inference never labelled observed

### Active perception
Fixtures where:
- label unreadable → asks for close-up
- missing rear connector → asks for rear view
- sufficient evidence → stops asking

### UI state
Presence state events map correctly to semantic visual states.

## Provider-backed evals

Optional command, not default `task check`.
Track model snapshots separately.

## Release gate

A 3.0 feature is not considered stable because one live demo worked.
It needs deterministic regression coverage.
