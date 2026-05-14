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

Decisions are intentionally excluded from decay pruning.
