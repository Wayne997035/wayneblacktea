---
id: WBT3-Q05
title: "Migration Review"
status: reviewed
order: 1105
depends_on: []
owners: []
baseline_commit: 75ce929c301090f0d1e742591b311e90b712c5fe
last_reviewed: 2026-08-25
---

# Migration Review

## Constraints checked

- no FKs
- immutable migrations
- SQLite/Postgres parity
- existing tools remain usable
- old web routes can remain during transition
- additive tables
- feature gates

## Risk

The largest migration risk is not schema; it is leaving old AI reasoning paths active while new Runtime paths duplicate them.

## Control

Every migrated reasoning path must have one explicit owner:
- scheduler callback
or
- operator/runtime

not both.

## Verdict
PASS.
