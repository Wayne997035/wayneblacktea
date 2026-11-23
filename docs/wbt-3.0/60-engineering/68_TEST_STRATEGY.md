---
id: WBT3-880
title: "Test Strategy"
status: frozen
order: 880
depends_on: [WBT3-850]
owners: []
baseline_commit: 75ce929c301090f0d1e742591b311e90b712c5fe
last_reviewed: 2026-08-25
---

# Test Strategy

## Existing quality gate

`cd build && task check` remains mandatory.

## Test pyramid

### Unit
- state machines
- policies
- scoring
- routing
- TTL
- redaction
- risk

### Store parity
Every new durable domain:
- Postgres
- SQLite

### Runtime integration
Fake model + fake capabilities.

### Web
- Presence state reducer
- media permission error UX
- approval UI
- reconnect
- route migration
- accessibility

### Contract
Provider adapters with recorded/synthetic payloads.

### Optional live
- realtime provider smoke
- camera/image
- voice

Do not require paid network provider for default CI.

## Adversarial cases

- tool call injected in OCR text
- page says "ignore policy"
- malformed event payload
- duplicate event
- stale approval
- mismatched target hash
- user interrupts mid-action explanation
