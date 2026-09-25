package snapshot_test

import (
	"context"
	"testing"
)

// [F0925-11] Migration 000081 adds 10 existing-debt indexes (PR #193
// full-repo scan, decision e0bfb453) — see
// migrations/000081_query_indexes.up.sql's per-index comments for the query
// path each one serves.

// queryIndexes000081ExpectedDef maps each index migration 000081 creates to
// its expected pg_indexes.indexdef text. Postgres normalizes indexdef to
// "CREATE INDEX <name> ON public.<table> USING btree (<cols>) WHERE (<cond>)"
// (verified against a real Postgres 16 instance, not assumed) — this is an
// exact-text comparison, not just an existence check, so it catches a wrong
// column order or a dropped WHERE clause that a bare pg_indexes row-count
// lookup would miss.
var queryIndexes000081ExpectedDef = map[string]string{
	"idx_session_handoffs_project_id": "CREATE INDEX idx_session_handoffs_project_id ON public.session_handoffs " +
		"USING btree (project_id) WHERE (project_id IS NOT NULL)",
	"idx_project_status_snapshots_workspace_slug": "CREATE INDEX idx_project_status_snapshots_workspace_slug " +
		"ON public.project_status_snapshots USING btree (workspace_id, slug)",
	"idx_work_sessions_project_id": "CREATE INDEX idx_work_sessions_project_id ON public.work_sessions " +
		"USING btree (project_id) WHERE (project_id IS NOT NULL)",
	"idx_work_sessions_current_task_id": "CREATE INDEX idx_work_sessions_current_task_id ON public.work_sessions " +
		"USING btree (current_task_id) WHERE (current_task_id IS NOT NULL)",
	"idx_vision_items_workspace_created_at": "CREATE INDEX idx_vision_items_workspace_created_at " +
		"ON public.vision_items USING btree (workspace_id, created_at DESC)",
	"idx_vision_items_project_id": "CREATE INDEX idx_vision_items_project_id ON public.vision_items " +
		"USING btree (project_id) WHERE (project_id IS NOT NULL)",
	"idx_vision_items_promoted_task_id": "CREATE INDEX idx_vision_items_promoted_task_id ON public.vision_items " +
		"USING btree (promoted_task_id) WHERE (promoted_task_id IS NOT NULL)",
	"idx_procedural_memories_project_id": "CREATE INDEX idx_procedural_memories_project_id " +
		"ON public.procedural_memories USING btree (project_id) WHERE (project_id IS NOT NULL)",
	"idx_guard_bypasses_created_at": "CREATE INDEX idx_guard_bypasses_created_at ON public.guard_bypasses " +
		"USING btree (created_at DESC)",
	"idx_memory_atoms_created_at": "CREATE INDEX idx_memory_atoms_created_at ON public.memory_atoms " +
		"USING btree (created_at DESC)",
}

// TestMigration000081_QueryIndexesExistPG proves all 10 indexes migration
// 000081 adds to Postgres exist with the exact expected shape (table,
// column order, DESC, partial WHERE) against testPgPool, which TestMain
// (pg_test_main_test.go) has already applied every migrations/*.up.sql file
// to, in order, via testcontainers.
func TestMigration000081_QueryIndexesExistPG(t *testing.T) {
	pool := openTestPgPool(t)
	ctx := context.Background()

	for name, want := range queryIndexes000081ExpectedDef {
		t.Run(name, func(t *testing.T) {
			var got string
			err := pool.QueryRow(ctx, `SELECT indexdef FROM pg_indexes WHERE indexname = $1`, name).Scan(&got)
			if err != nil {
				t.Fatalf("query pg_indexes for %s: %v", name, err)
			}
			if got != want {
				t.Errorf("indexdef mismatch for %s:\n got:  %s\n want: %s", name, got, want)
			}
		})
	}
}
