---
id: WBT3-300
title: "Presence Session"
status: frozen
order: 300
depends_on: [WBT3-100, WBT3-110]
owners: []
baseline_commit: 75ce929c301090f0d1e742591b311e90b712c5fe
last_reviewed: 2026-08-25
---

# Presence Session

## Purpose

A Presence Session is the user's live collaboration connection to WBT.

It combines:
- text
- voice
- optional camera
- optional screen
- live runtime status
- client capability state

## State machine

```text
DISCONNECTED
→ PREPARING
→ READY
→ LISTENING / OBSERVING
→ REASONING
→ ACTING
→ VERIFYING
→ SPEAKING
→ READY

special:
WAITING_APPROVAL
INTERRUPTED
DEGRADED
RECONNECTING
ENDED
```

## Backend control channel

Web client maintains authenticated same-origin control connection to WBT for:
- presence state
- context updates
- capability intent
- approvals
- trace events
- operator notifications

## Provider media path

When provider supports safe direct client sessions:

```text
Browser ── short-lived token ── Provider
```

Backend still owns:
- bootstrap
- system context constraints
- action authorization
- durable state

## Browser lifecycle

Presence media does not imply an always-on OS service.

If page/client closes:
- server operators continue
- Presence Session ends
- camera/mic/screen observations stop

## Session recovery

Reconnect may restore:
- concise conversation summary
- recent run results
- current work context

Do not depend on provider conversation history as durable memory.
