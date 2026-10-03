//go:build integration

package storage

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// [F1003-09] Postgres DDL/reversibility + EXPLAIN coverage for migration
// 000087 — see migrations/000087_pending_proposals_status_pending_sort_key.up.sql
// for the index shape and rationale, and
// internal/storage/sqlite/migration_000087_index_test.go for the SQLite
// twin. package storage (not storage_test), same build-tag `integration`
// file group as repo_name_cleanup_migration_integration_test.go — reuses
// its TestMain / testAdminDSN / newIsolatedTestDB / migrateToVersion.
//
// Unlike 000086, this index's EXPLAIN proof does not need the unexported
// sqlc listPendingProposals constant to demonstrate the sort-elimination
// behavior cleanly: the predicate under test (status = 'pending', no
// workspace_id in the index at all) is simple enough to write directly and
// still exercise the real index-usage question (does the id DESC
// tiebreaker remove the Sort node), so this file carries both the DDL
// proof and the EXPLAIN proof for 000087. The cost-threshold /
// generic-plan judgment methodology (Lead's supplement 1) is applied to
// 000087 in internal/db/migration_000087_explain_test.go, alongside 000086.

// wantIdx000087DefOld / wantIdx000087DefNew are the exact expected
// pg_indexes.indexdef text before/after migration 000087 — verified against
// a real Postgres 16 instance, not assumed.
const (
	wantIdx000087DefOld = "CREATE INDEX idx_pending_proposals_status_pending " +
		"ON public.pending_proposals USING btree (created_at DESC) WHERE (status = 'pending'::text)"
	wantIdx000087DefNew = "CREATE INDEX idx_pending_proposals_status_pending " +
		"ON public.pending_proposals USING btree (created_at DESC, id DESC) WHERE (status = 'pending'::text)"
)

func pendingProposalsIndexDef(t *testing.T, ctx context.Context, pool *pgxpool.Pool) string {
	t.Helper()
	var got string
	if err := pool.QueryRow(
		ctx,
		`SELECT indexdef FROM pg_indexes WHERE indexname = $1`,
		"idx_pending_proposals_status_pending",
	).Scan(&got); err != nil {
		t.Fatalf("query pg_indexes: %v", err)
	}
	return got
}

// TestMigration000087_PendingProposalsSortKeyIndexExists proves migration
// 000087 realigns idx_pending_proposals_status_pending to the new column
// list, with down restoring the OLD indexdef text (not dropping the index
// entirely — this is a realignment, not a net-new object).
func TestMigration000087_PendingProposalsSortKeyIndexExists(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Docker")
	}
	ctx := context.Background()
	dsn := newIsolatedTestDB(t, testAdminDSN)
	migrateToVersion(t, dsn, 86)

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	defer pool.Close()

	if got := pendingProposalsIndexDef(t, ctx, pool); got != wantIdx000087DefOld {
		t.Fatalf("pre-087 indexdef mismatch:\n got:  %s\n want: %s", got, wantIdx000087DefOld)
	}

	t.Setenv("WBT_AUTO_MIGRATE", "")
	if err := RunMigrations(ctx, dsn); err != nil {
		t.Fatalf("RunMigrations to 87: %v", err)
	}
	if got := pendingProposalsIndexDef(t, ctx, pool); got != wantIdx000087DefNew {
		t.Errorf("post-087 indexdef mismatch:\n got:  %s\n want: %s", got, wantIdx000087DefNew)
	}

	migrateToVersion(t, dsn, 86)
	if got := pendingProposalsIndexDef(t, ctx, pool); got != wantIdx000087DefOld {
		t.Errorf("post-down indexdef mismatch (want the OLD shape restored):\n got:  %s\n want: %s", got, wantIdx000087DefOld)
	}

	migrateToVersion(t, dsn, 87)
	if got := pendingProposalsIndexDef(t, ctx, pool); got != wantIdx000087DefNew {
		t.Errorf("post-re-up indexdef mismatch:\n got:  %s\n want: %s", got, wantIdx000087DefNew)
	}
}

// wantIdxWorkspacePendingSortDef is the exact expected pg_indexes.indexdef
// text for the net-new workspace-leading composite (decision 7a064608,
// post-STOP follow-up) — verified against a real Postgres 16 instance, not
// assumed.
const wantIdxWorkspacePendingSortDef = "CREATE INDEX idx_pending_proposals_workspace_pending_sort " +
	"ON public.pending_proposals USING btree (workspace_id, created_at DESC, id DESC) WHERE (status = 'pending'::text)"

// TestMigration000087_WorkspacePendingSortIndexExists is the decision
// 7a064608 follow-up: proves idx_pending_proposals_workspace_pending_sort
// is absent pre-087, exists with the exact expected shape post-087, and is
// fully reversible (down drops it entirely — net-new, unlike the realigned
// idx_pending_proposals_status_pending).
func TestMigration000087_WorkspacePendingSortIndexExists(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Docker")
	}
	ctx := context.Background()
	dsn := newIsolatedTestDB(t, testAdminDSN)
	migrateToVersion(t, dsn, 86)

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	defer pool.Close()

	indexDef := func(name string) (string, bool) {
		var got string
		err := pool.QueryRow(ctx, `SELECT indexdef FROM pg_indexes WHERE indexname = $1`, name).Scan(&got)
		if err != nil {
			return "", false
		}
		return got, true
	}

	if _, ok := indexDef("idx_pending_proposals_workspace_pending_sort"); ok {
		t.Fatal("idx_pending_proposals_workspace_pending_sort exists before migration 000087 has run")
	}

	t.Setenv("WBT_AUTO_MIGRATE", "")
	if err := RunMigrations(ctx, dsn); err != nil {
		t.Fatalf("RunMigrations to 87: %v", err)
	}
	got, ok := indexDef("idx_pending_proposals_workspace_pending_sort")
	if !ok {
		t.Fatal("idx_pending_proposals_workspace_pending_sort missing after migration 000087")
	}
	if got != wantIdxWorkspacePendingSortDef {
		t.Errorf("indexdef mismatch:\n got:  %s\n want: %s", got, wantIdxWorkspacePendingSortDef)
	}

	migrateToVersion(t, dsn, 86)
	if _, ok := indexDef("idx_pending_proposals_workspace_pending_sort"); ok {
		t.Error("idx_pending_proposals_workspace_pending_sort still exists after the down migration")
	}

	migrateToVersion(t, dsn, 87)
	got, ok = indexDef("idx_pending_proposals_workspace_pending_sort")
	if !ok {
		t.Fatal("idx_pending_proposals_workspace_pending_sort missing after re-up")
	}
	if got != wantIdxWorkspacePendingSortDef {
		t.Errorf("indexdef mismatch after re-up:\n got:  %s\n want: %s", got, wantIdxWorkspacePendingSortDef)
	}
}

// TestMigration000087_PendingProposalsSortKeyIndexUsedByQuery is the
// EXPLAIN proof that the realigned index removes the Sort node the pre-087
// shape needed, under SET LOCAL enable_seqscan = off (same technique as
// internal/storage/migration_000085_index_integration_test.go) — this
// isolates "does the index shape itself support the sort" from "would the
// planner pick it at this table's tiny test-row-count regardless of shape".
func TestMigration000087_PendingProposalsSortKeyIndexUsedByQuery(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Docker")
	}
	ctx := context.Background()
	dsn := newIsolatedTestDB(t, testAdminDSN)
	migrateToVersion(t, dsn, 86)

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	defer pool.Close()

	wsID := uuid.New()
	for i := 0; i < 5; i++ {
		if _, err := pool.Exec(
			ctx,
			`INSERT INTO pending_proposals (id, workspace_id, type, payload, status, created_at)
			 VALUES ($1, $2, 'task', '{}'::jsonb, 'pending', NOW())`,
			uuid.New(), wsID,
		); err != nil {
			t.Fatalf("seed pending_proposals row %d: %v", i, err)
		}
	}

	explain := func() string {
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatalf("begin tx: %v", err)
		}
		defer func() { _ = tx.Rollback(ctx) }()
		if _, err := tx.Exec(ctx, `SET LOCAL enable_seqscan = off`); err != nil {
			t.Fatalf("SET LOCAL enable_seqscan = off: %v", err)
		}
		rows, err := tx.Query(ctx, `EXPLAIN SELECT id FROM pending_proposals
			WHERE status = 'pending'
			ORDER BY created_at DESC, id DESC
			LIMIT 20`)
		if err != nil {
			t.Fatalf("EXPLAIN query: %v", err)
		}
		var lines []string
		for rows.Next() {
			var line string
			if err := rows.Scan(&line); err != nil {
				rows.Close()
				t.Fatalf("scan EXPLAIN line: %v", err)
			}
			lines = append(lines, line)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			t.Fatalf("iterate EXPLAIN rows: %v", err)
		}
		return strings.Join(lines, "\n")
	}

	prePlan := explain()
	t.Logf("pre-087 EXPLAIN:\n%s", prePlan)
	if !strings.Contains(prePlan, "Sort") {
		t.Fatalf("expected a Sort node pre-087 (old index has no id tiebreaker), got:\n%s", prePlan)
	}

	t.Setenv("WBT_AUTO_MIGRATE", "")
	if err := RunMigrations(ctx, dsn); err != nil {
		t.Fatalf("RunMigrations to 87: %v", err)
	}
	postPlan := explain()
	t.Logf("post-087 EXPLAIN:\n%s", postPlan)
	// "Index Only Scan" (observed, verified against a real instance) rather
	// than "Index Scan": the SELECT list (id only) is fully covered by the
	// index plus its visibility map, so the planner skips the heap fetch
	// entirely — an even better outcome than a plain Index Scan, still
	// proving the realigned index serves the query with no Sort node.
	if !strings.Contains(postPlan, "idx_pending_proposals_status_pending") ||
		!strings.Contains(postPlan, "Index") {
		t.Fatalf("expected an Index (Only) Scan using idx_pending_proposals_status_pending, got:\n%s", postPlan)
	}
	if strings.Contains(postPlan, "Seq Scan") {
		t.Fatalf("post-087 plan fell back to a Seq Scan:\n%s", postPlan)
	}
	if strings.Contains(postPlan, "Sort") {
		t.Fatalf("post-087 plan still has a Sort node — the id DESC tiebreaker should have removed it:\n%s", postPlan)
	}
}
