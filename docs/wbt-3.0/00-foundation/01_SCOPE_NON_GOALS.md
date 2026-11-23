---
id: WBT3-001
title: "Scope and Non-goals"
status: frozen
order: 11
depends_on: [WBT3-000]
owners: []
baseline_commit: 75ce929c301090f0d1e742591b311e90b712c5fe
last_reviewed: 2026-08-25
---

# Scope and Non-goals

## 3.0 required scope

### Interaction
- Native WBT text conversation
- Native WBT voice conversation
- interruption / barge-in
- image upload
- camera session
- screen-share perception when the host surface supports it
- active perception requests ("closer", "back side", "show the error")

### Intelligence
- native multi-turn Runtime
- ContextPack integration
- model/provider routing
- tool/capability loop
- delegation from realtime model to deeper reasoning model
- evidence/uncertainty handling

### Action
- central Capability Gateway
- risk classification
- policy enforcement
- approval flow
- idempotency and verification
- trace

### Autonomy
- Event Fabric
- World Model
- Attention Engine
- Persistent Operators
- proactive notifications/proposals

### Web 3.0
- Presence
- World
- Mind
- Operations
- Control
- Explore/legacy data views
- semantic runtime visualization

## Optional distribution scope

A macOS/desktop client is allowed if it can reuse the same Go + React/TypeScript implementation boundary, for example through a Go webview shell.

It is a **WBT Desktop Client**, not a separate AI identity.

## Explicit 3.0 non-goals

- Requiring Swift / Objective-C code as part of the WBT 3.0 core deliverable
- OS-level always-on camera or screen recording
- robotics motion control
- hardware driver development
- autonomous high-risk production changes without policy/approval
- training foundation models
- implementing CUDA/GPU inference runtimes
- replacing Postgres/SQLite with a graph database
- replacing WBT structured state with generic conversation memory
- creating emotional-dependency or relationship-gamification mechanics
- simulating feelings to drive engagement

## Critical web limitation

A browser is not an OS daemon.

Server-side operators can remain active when the browser is closed, but user camera/microphone/screen perception exists only while an authorized Presence Session is active on a supporting client.

Screen capture permission must be treated as explicit/session-bound. No spec may claim permanent browser screen permission.
