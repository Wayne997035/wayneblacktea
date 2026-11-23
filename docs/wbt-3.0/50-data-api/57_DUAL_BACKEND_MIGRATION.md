---
id: WBT3-770
title: "Dual-backend Migration Rules"
status: frozen
order: 770
depends_on: [WBT3-700]
owners: []
baseline_commit: 75ce929c301090f0d1e742591b311e90b712c5fe
last_reviewed: 2026-08-25
---

# Dual-backend Migration Rules

Existing repo hard rules remain:

- migrations immutable after merge
- no FK constraints
- Postgres/SQLite parity
- add new numbered migration only
- update both storage implementations
- deterministic tests

## Recommended sequence

Do not create all 3.0 tables in one giant migration.

Suggested independent migrations:
1. runtime/trace
2. capability approval
3. operators
4. events
5. world entities/beliefs
6. optional presence session metadata

Each phase must be deployable with dormant features.

## Rollback

Code must tolerate:
- feature disabled
- empty tables
- provider not configured

Database down migrations are not a substitute for application rollback planning.
