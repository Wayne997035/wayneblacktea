---
id: WBT3-930
title: "Recommended PR Sequence"
status: frozen
order: 930
depends_on: [WBT3-900]
owners: []
baseline_commit: 75ce929c301090f0d1e742591b311e90b712c5fe
last_reviewed: 2026-08-25
---

# Recommended PR Sequence

Avoid giant branches.

1. `docs/wbt-3-spec-freeze`
2. `refactor/risk-taxonomy`
3. `feature/runtime-core`
4. `feature/runtime-trace`
5. `feature/llm-generate-router`
6. `feature/capability-gateway`
7. `feature/policy-approvals`
8. `feature/presence-text`
9. `feature/web-3-shell`
10. `feature/realtime-voice`
11. `feature/perception-image-camera`
12. `feature/perception-screen`
13. `feature/active-perception`
14. `feature/event-fabric`
15. `feature/world-model`
16. `feature/attention-engine`
17. `feature/operators`
18. `feature/3.0-evals-hardening`
19. `release/3.0-gates`

## Merge rule

Each PR:
- compiles independently
- feature disabled safely if incomplete
- includes tests
- does not rely on unmerged migration mutation
- updates architecture docs

Do not split Postgres and SQLite support into separate merge windows for user-visible domains.
