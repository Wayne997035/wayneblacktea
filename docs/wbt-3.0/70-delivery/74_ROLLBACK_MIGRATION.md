---
id: WBT3-940
title: "Rollback and Migration Strategy"
status: frozen
order: 940
depends_on: [WBT3-900]
owners: []
baseline_commit: 75ce929c301090f0d1e742591b311e90b712c5fe
last_reviewed: 2026-08-25
---

# Rollback and Migration Strategy

## Feature flags / config gates

New subsystems start disabled or inert when not configured:
- Runtime provider
- realtime provider
- perception
- event adapters
- operators

## Database compatibility

New tables are additive.

Old 2.x clients/tools continue to work while 3.0 runtime is being introduced.

## Web rollback

Keep legacy dashboard component/routes until Presence is stable.
A config/route flag can restore legacy `/` during rollout.

## Provider rollback

Model/provider adapter can be disabled independently.

## Operator rollback

Pause/disable manifest without deleting state.

## Emergency

Global external-action kill switch:
- blocks WRITE_EXTERNAL/DESTRUCTIVE
- keeps read-only state/UI available

## Migration principle

Never require destructive data migration just to adopt 3.0 UI/runtime.
