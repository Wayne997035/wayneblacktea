---
id: WBT3-510
title: "Presence Surface"
status: frozen
order: 510
depends_on: [WBT3-500]
owners: []
baseline_commit: 75ce929c301090f0d1e742591b311e90b712c5fe
last_reviewed: 2026-08-25
---

# Presence Surface

## Layout

Desktop:

```text
┌────────────────────────────────────────────────────────────────┐
│ WBT 3.0     context · provider · privacy · connection          │
├──────────┬──────────────────────────────────┬───────────────────┤
│ Context  │                                  │ Live activity     │
│ / focus  │           WBT CORE               │ trace summary     │
│          │                                  │                   │
│ active   │  transcript / visual workspace   │ current run       │
│ work     │                                  │ action / verify   │
│          │                                  │                   │
├──────────┴──────────────────────────────────┴───────────────────┤
│ input…                         [mic] [camera] [screen] [send]   │
└────────────────────────────────────────────────────────────────┘
```

## Central Core

Not decoration.

Maps real states:
- READY
- LISTENING
- OBSERVING
- RETRIEVING
- REASONING
- ACTING
- VERIFYING
- SPEAKING
- WAITING_APPROVAL
- DEGRADED

## Live activity

Human-readable trace:
- loaded project context
- reading CI log
- checking previous decision
- requested approval
- verification passed

Do not expose hidden chain-of-thought.

## Media controls

Persistent visible status:
- microphone on/off
- camera source
- screen source
- cloud/local routing
- stop sharing

## Input

Text input remains first-class even when voice is active.
