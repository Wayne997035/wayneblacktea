---
id: WBT3-740
title: "Realtime Control Protocol"
status: frozen
order: 740
depends_on: [WBT3-300, WBT3-730]
owners: []
baseline_commit: 75ce929c301090f0d1e742591b311e90b712c5fe
last_reviewed: 2026-08-25
---

# Realtime Control Protocol

## Control channel messages

Server → client:
- `presence.state`
- `runtime.run_started`
- `runtime.step`
- `runtime.result`
- `action.approval_required`
- `operator.notification`
- `context.updated`
- `session.degraded`

Client → server:
- `user.text`
- `user.interrupt`
- `media.state`
- `capability.intent`
- `approval.response`
- `observation.submit`

## Intent trust

Client-supplied capability intent is authenticated input, not model-proof.

Server:
- validates session
- validates declared capability
- policy checks
- traces provenance as `client_mediated`

## Sequence numbers

Control messages include monotonically increasing sequence per presence session for:
- ordering
- reconnect
- duplicate suppression

## Backpressure

Do not stream every internal trace/log line.
Use bounded semantic step events.

## Reconnect

Client sends last acknowledged sequence.
Server may replay bounded recent state or issue snapshot.
