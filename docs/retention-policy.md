# Retention Policy

wayneblacktea keeps durable user knowledge and decisions, but prunes operational
telemetry so the single-tenant database does not grow without bound.

As of the `chore/scheduler-prune-factory` refactor (commit `501969e`), the daily
TTL-prune jobs tagged `PrunerSpec{...}` below run through one shared registry —
`scheduler.WithPruner` / `runPrune` (`internal/scheduler/scheduler.go:163-232`) —
instead of one bespoke method per table; each row's retention/schedule values
now live in that table's `PrunerSpec` literal in `cmd/server/main.go`.

| Table | TTL | Enforcement |
|-------|-----|-------------|
| `guard_events` | 30 days | `scheduler.runDailyGuardPrune` at 23:30 Asia/Taipei |
| `guard_bypasses` | 30 days by `created_at` | `scheduler.runDailyGuardPrune` at 23:30 Asia/Taipei |
| `discipline_events` | 30 days | `scheduler.runDailyDisciplinePrune` at 23:00 Asia/Taipei |
| `pending_proposals` | accepted/rejected: 90 days; pending `type='decision'`: 180 days | `scheduler.runDailyPendingProposalsPrune` at 03:00 Asia/Taipei |
| `knowledge_items` | Ebbinghaus decay, low-importance rows only | `decay.Pruner` daily decay job |
| `concepts` | Ebbinghaus decay, low-importance rows only | `decay.Pruner` daily decay job |
| `memory_atoms` | 90 days for low-value atoms | `decay.Pruner` via atom store pruning |
| `session_handoffs`[^3] | Open (unresolved) rows kept forever; resolved rows pruned after 365 days (`PrunerSpec{Name:"session_handoff"}`, `cmd/server/main.go:546-554`) | `resolve_handoff` / auto-handoff lifecycle; `scheduler.runPrune`, job `daily-session_handoff-prune` at 04:45 Asia/Taipei |
| `outcomes` + `evaluations` | 90 days (`PrunerSpec{Name:"outcome"}`, `cmd/server/main.go:458-466`) | `scheduler.runPrune`, job `daily-outcome-prune` at 03:30 Asia/Taipei (`scheduler.go:190-232`) |
| `reflections` | 180 days (`PrunerSpec{Name:"reflection"}`, `cmd/server/main.go:472-480`) | `scheduler.runPrune`, job `daily-reflection-prune` at 03:45 Asia/Taipei (`scheduler.go:190-232`) |
| `behavior_rules`[^1] | 365 days (`PrunerSpec{Name:"behavior_rule"}`, `cmd/server/main.go:487-495`) | `scheduler.runPrune`, job `daily-behavior_rule-prune` at 04:00 Asia/Taipei (`scheduler.go:190-232`) |
| `discipline_events_m8` | 90 days (`PrunerSpec{Name:"discipline_event_m8"}`, `cmd/server/main.go:594-602`) | `scheduler.runPrune`, job `daily-discipline_event_m8-prune` at 03:50 Asia/Taipei (`scheduler.go:190-232`) |
| `ai_cost_ledger`[^2] | 30 days (`PrunerSpec{Name:"ai_cost_ledger"}`, `cmd/server/main.go:607-615`) | `scheduler.runPrune`, job `daily-ai_cost_ledger-prune` at 04:15 Asia/Taipei (`scheduler.go:190-232`) |
| `work_session_evidence` | 90 days (`PrunerSpec{Name:"work_session_evidence"}`, `cmd/server/main.go:503-511`) | `scheduler.runPrune`, job `daily-work_session_evidence-prune` at 04:20 Asia/Taipei (`scheduler.go:190-232`) |
| `activity_log` | 365 days (`PrunerSpec{Name:"activity_log"}`, `cmd/server/main.go:519-527`) | `scheduler.runPrune`, job `daily-activity_log-prune` at 04:35 Asia/Taipei (`scheduler.go:190-232`) |
| `project_status_snapshots`[^4] | 180 days (`PrunerSpec{Name:"project_status_snapshot"}`, `cmd/server/main.go:533-541`) | `scheduler.runPrune`, job `daily-project_status_snapshot-prune` at 04:40 Asia/Taipei (`scheduler.go:190-232`) |

Decisions are intentionally excluded from decay pruning.

[^1]: The `behavior_rules` prune job only deletes rows where `status IN ('rejected', 'deprecated')` and `created_at` is older than the 365-day cutoff (`internal/storage/sqlite/behaviorrule.go:258-274`). Rows with `status='active'` or `status='proposed'` are **never** auto-pruned regardless of age — they are only removed via an explicit `Deprecate` call, which itself only relabels the status (the row still isn't deleted until it ages past the cutoff).
[^2]: The `ai_cost_ledger` prune job is **Postgres-only**: `NewAICostLedgerPrunerAdapter` wraps a `*pgxpool.Pool` directly (`internal/scheduler/scheduler.go`, adapter for the shared `PrunerStore` interface) and issues a parameterized `DELETE ... WHERE created_at < $1`, with the 30-day cutoff computed app-side (`time.Now().Add(-retention)`) rather than via Postgres `NOW() - INTERVAL` as before the `chore/scheduler-prune-factory` refactor (commit `501969e`) — needed to conform to the shared `PrunerStore.PruneOlderThan(ctx, cutoff time.Time)` contract used by every registry-driven prune job. There is no SQLite equivalent registered for this table.
[^3]: `session.Store.PruneOlderThan` only deletes rows where `resolved_at IS NOT NULL` and `resolved_at` is older than the cutoff (`internal/session/store.go:411-423`). Open handoffs (`resolved_at IS NULL`) are never deleted regardless of age — they are the "what to continue" signal the next session reads.
[^4]: The `project_status_snapshots` prune job is **Postgres-only**: there is no SQLite implementation of `snapshot.StoreIface`, so the pruner is only wired when a Postgres snapshot store is configured.
