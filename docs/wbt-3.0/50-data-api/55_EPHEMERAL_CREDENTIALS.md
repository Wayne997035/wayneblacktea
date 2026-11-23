---
id: WBT3-750
title: "Realtime Provider Credential Architecture"
status: frozen
order: 750
depends_on: [WBT3-130, WBT3-300]
owners: []
baseline_commit: 75ce929c301090f0d1e742591b311e90b712c5fe
last_reviewed: 2026-08-25
---

# Realtime Provider Credential Architecture

## Rule

Long-lived provider credentials never enter browser bundles, localStorage or JS-readable WBT auth state.

## Flow

```text
Browser authenticated to WBT
→ POST realtime credential
→ WBT backend requests provider short-lived/scoped credential
→ browser opens direct realtime connection
→ credential expires
```

## Provider constraints

Adapters declare:
- credential TTL
- one-use/session constraints
- model/session config constraints
- refresh/resumption support

## Security

- bind credential bootstrap to authenticated Presence Session
- rate limit creation
- restrict model/modalities when provider supports it
- never log token
- avoid returning provider master API key under any condition

## Fallback

If provider lacks safe client credential:
- use backend-proxied realtime connection, or
- do not support direct client mode for that provider

Do not solve this by embedding a permanent key.
