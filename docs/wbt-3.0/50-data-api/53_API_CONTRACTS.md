---
id: WBT3-730
title: "API Contracts"
status: frozen
order: 730
depends_on: [WBT3-110, WBT3-300, WBT3-700]
owners: []
baseline_commit: 75ce929c301090f0d1e742591b311e90b712c5fe
last_reviewed: 2026-08-25
---

# API Contracts

## Runtime

```text
POST /api/runtime/runs
GET  /api/runtime/runs/:id
POST /api/runtime/runs/:id/cancel
```

## Presence

```text
POST /api/presence/sessions
POST /api/presence/sessions/:id/realtime-credential
POST /api/presence/sessions/:id/end
GET/WS /api/presence/sessions/:id/control
```

## Capability/approval

```text
POST /api/runtime/actions/:id/approve
POST /api/runtime/actions/:id/reject
```

## World

```text
GET /api/world/entities
GET /api/world/entities/:id
GET /api/world/events
POST /api/world/entities/:id/confirm
```

## Operators

```text
GET  /api/operators
POST /api/operators/:id/run
POST /api/operators/:id/pause
POST /api/operators/:id/resume
```

## Trace

```text
GET /api/runtime/runs/:id/trace
```

## Rules

- same-origin browser auth
- strict request limits
- UUID validation
- no raw provider secret in response except short-lived scoped realtime credentials
- HTTP handlers invoke domain services; no direct model/tool logic in handlers
