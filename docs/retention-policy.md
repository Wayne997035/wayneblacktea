# Retention Policy

wayneblacktea keeps durable user knowledge and decisions, but prunes operational
telemetry so the single-tenant database does not grow without bound.

| Table | TTL | Enforcement |
|-------|-----|-------------|
| `guard_events` | 30 days | `scheduler.runDailyGuardPrune` at 23:30 Asia/Taipei |
| `guard_bypasses` | 30 days by `created_at` | `scheduler.runDailyGuardPrune` at 23:30 Asia/Taipei |
| `discipline_events` | 30 days | `scheduler.runDailyDisciplinePrune` at 23:00 Asia/Taipei |
| `pending_proposals` | accepted/rejected: 90 days; pending `type='decision'`: 180 days | `scheduler.runDailyPendingProposalsPrune` at 03:00 Asia/Taipei |
| `knowledge_items` | Ebbinghaus decay, low-importance rows only | `decay.Pruner` daily decay job |
| `concepts` | Ebbinghaus decay, low-importance rows only | `decay.Pruner` daily decay job |
| `memory_atoms` | 90 days for low-value atoms | `decay.Pruner` via atom store pruning |
| `session_handoffs` | Kept until resolved; current handoff selected by `resolved_at IS NULL` | `resolve_handoff` / auto-handoff lifecycle |
| `outcomes` + `evaluations` | 90 days (`outcomePruneRetention`, `scheduler.go:201`) | `scheduler.runDailyOutcomePrune`, job `daily-outcome-prune` at 03:30 Asia/Taipei (`scheduler.go:729-738`) |
| `reflections` | 180 days (`reflectionPruneRetention`, `scheduler.go:215`) | `scheduler.runDailyReflectionPrune`, job `daily-reflection-prune` at 03:45 Asia/Taipei (`scheduler.go:768-781`) |
| `behavior_rules`[^1] | 365 days (`behaviorRulePruneRetention`, `scheduler.go:231`) | `scheduler.runDailyBehaviorRulePrune`, job `daily-behavior-rule-prune` at 04:00 Asia/Taipei (`scheduler.go:810-823`) |
| `discipline_events_m8` | 90 days (`disciplineEventM8PruneRetention`, `scheduler.go:1164`) | `scheduler.runDailyDisciplineEventM8Prune`, job `daily-discipline-event-m8-prune` at 03:50 Asia/Taipei (`scheduler.go:1172-1185`) |
| `ai_cost_ledger`[^2] | 30 days (`aiCostPruneAge`, `scheduler.go:32`) | `scheduler.runDailyAICostLedgerPrune`, job `daily-ai-cost-ledger-prune` at 04:15 Asia/Taipei (`scheduler.go:1212-1225`) |

Decisions are intentionally excluded from decay pruning.

[^1]: The `behavior_rules` prune job only deletes rows where `status IN ('rejected', 'deprecated')` and `created_at` is older than the 365-day cutoff (`internal/storage/sqlite/behaviorrule.go:258-274`). Rows with `status='active'` or `status='proposed'` are **never** auto-pruned regardless of age — they are only removed via an explicit `Deprecate` call, which itself only relabels the status (the row still isn't deleted until it ages past the cutoff).
[^2]: The `ai_cost_ledger` prune job is **Postgres-only**: `WithAICostLedgerPruner` takes a `*pgxpool.Pool` directly (`scheduler.go:1212`) and issues a raw `DELETE ... WHERE created_at < NOW() - INTERVAL '30 days'`. There is no SQLite equivalent registered for this table.
