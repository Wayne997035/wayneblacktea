//go:build integration

package storage

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// [F0930-06] Postgres coverage for migration 000085 — see
// migrations/000085_pending_proposals_source_entity_idx.up.sql for the
// index shape and rationale, and
// internal/storage/sqlite/migration_000085_index_test.go for the SQLite
// twin these tests mirror. package storage (not storage_test), same
// build-tag `integration` file group as
// repo_name_cleanup_migration_integration_test.go, so this file reuses that
// file's TestMain / testAdminDSN / newIsolatedTestDB / migrateToVersion —
// do NOT declare a second TestMain in this package.

// wantIdx000085Def is the exact expected pg_indexes.indexdef text — verified
// against a real Postgres 16 instance (pgvector/pgvector:pg16, same image
// this package's TestMain uses), not assumed. Column order (type,
// proposed_by, expression) matches the dedup query's three equality
// predicates exactly (internal/scheduler/cognitive_jobs.go's
// pgDecisionsPendingOutcomeReview).
const wantIdx000085Def = "CREATE INDEX idx_pending_proposals_type_proposer_source " +
	"ON public.pending_proposals USING btree (type, proposed_by, ((payload ->> 'source_entity_id'::text)))"

// TestMigration000085_ProposalDedupIndexExists proves migration 000085 adds
// idx_pending_proposals_type_proposer_source to Postgres with the exact
// expected shape (table, column order, expression) — an exact-text
// comparison, not just an existence check, so a wrong column order or a
// dropped column is caught, not just a missing index (mirrors
// internal/snapshot/migration_000081_indexes_test.go's technique).
func TestMigration000085_ProposalDedupIndexExists(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Docker")
	}
	ctx := context.Background()
	dsn := newIsolatedTestDB(t, testAdminDSN)
	migrateToVersion(t, dsn, 84)

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	defer pool.Close()

	var existsBefore bool
	if err := pool.QueryRow(
		ctx,
		`SELECT EXISTS (SELECT 1 FROM pg_indexes WHERE indexname = $1)`,
		"idx_pending_proposals_type_proposer_source",
	).Scan(&existsBefore); err != nil {
		t.Fatalf("check index before migration: %v", err)
	}
	if existsBefore {
		t.Fatal("idx_pending_proposals_type_proposer_source exists before migration 000085 has run")
	}

	t.Setenv("WBT_AUTO_MIGRATE", "")
	if err := RunMigrations(ctx, dsn); err != nil {
		t.Fatalf("RunMigrations to 85: %v", err)
	}

	var got string
	if err := pool.QueryRow(
		ctx,
		`SELECT indexdef FROM pg_indexes WHERE indexname = $1`,
		"idx_pending_proposals_type_proposer_source",
	).Scan(&got); err != nil {
		t.Fatalf("query pg_indexes: %v", err)
	}
	if got != wantIdx000085Def {
		t.Errorf("indexdef mismatch:\n got:  %s\n want: %s", got, wantIdx000085Def)
	}

	// down.sql only drops this one index — migrating back to 84 must remove
	// it, and re-migrating to 85 must recreate it with the identical shape
	// (reversibility, mirrors TestMigration000081_QueryIndexesExist).
	migrateToVersion(t, dsn, 84)
	var existsAfterDown bool
	if err := pool.QueryRow(
		ctx,
		`SELECT EXISTS (SELECT 1 FROM pg_indexes WHERE indexname = $1)`,
		"idx_pending_proposals_type_proposer_source",
	).Scan(&existsAfterDown); err != nil {
		t.Fatalf("check index after down: %v", err)
	}
	if existsAfterDown {
		t.Error("idx_pending_proposals_type_proposer_source still exists after the down migration")
	}

	migrateToVersion(t, dsn, 85)
	var gotAfterReup string
	if err := pool.QueryRow(
		ctx,
		`SELECT indexdef FROM pg_indexes WHERE indexname = $1`,
		"idx_pending_proposals_type_proposer_source",
	).Scan(&gotAfterReup); err != nil {
		t.Fatalf("query pg_indexes after re-up: %v", err)
	}
	if gotAfterReup != wantIdx000085Def {
		t.Errorf("indexdef mismatch after re-up:\n got:  %s\n want: %s", gotAfterReup, wantIdx000085Def)
	}
}

// TestMigration000085_ProposalDedupIndexUsedByQuery is the EXPLAIN proof
// (D10 ambiguity resolution) that
// idx_pending_proposals_type_proposer_source is not just present but
// actually usable for the dedup predicate shape
// pgDecisionsPendingOutcomeReview's NOT EXISTS subquery filters on: type,
// proposed_by, AND the payload->>'source_entity_id' expression, all three
// in the Index Cond — none of them relegated to a Filter (which would mean
// the index only partially covers the predicate). `SET LOCAL
// enable_seqscan = off` makes this deterministic regardless of the test
// table's tiny row count (verified manually against a real Postgres 16
// instance: at personal-OS test scale, cost-based planning alone picks a
// Seq Scan below roughly 200 rows even with the index present — forcing
// seqscan off is what actually exercises the index path, the same
// technique used to test index correctness independent of data volume).
func TestMigration000085_ProposalDedupIndexUsedByQuery(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Docker")
	}
	ctx := context.Background()
	dsn := newIsolatedTestDB(t, testAdminDSN)
	migrateToVersion(t, dsn, 85)

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	defer pool.Close()

	wsID := uuid.New()
	migration000085ProposalDedupIndexUsedByQuerySeed(t, ctx, pool, wsID)

	plan, planLines := migration000085ProposalDedupIndexUsedByQueryExplainPlan(t, ctx, pool)

	if !strings.Contains(plan, "Index Scan using idx_pending_proposals_type_proposer_source") {
		t.Fatalf("expected an Index Scan using idx_pending_proposals_type_proposer_source, got:\n%s", plan)
	}

	migration000085ProposalDedupIndexUsedByQueryAssertIndexCond(t, plan, planLines)
	migration000085ProposalDedupIndexUsedByQueryAssertNoResidualFilter(t, planLines)
}

// migration000085ProposalDedupIndexUsedByQuerySeed seeds one pending_proposals
// row shaped like the scheduler's dedup probe.
func migration000085ProposalDedupIndexUsedByQuerySeed(t *testing.T, ctx context.Context, pool *pgxpool.Pool, wsID uuid.UUID) {
	t.Helper()
	if _, err := pool.Exec(
		ctx, `INSERT INTO pending_proposals
		(id, workspace_id, type, payload, status, proposed_by, created_at)
		VALUES ($1, $2, 'task', $3, 'pending', 'scheduler:decision_outcome_review', NOW())`,
		uuid.New(), wsID, `{"source_entity_id":"probe"}`,
	); err != nil {
		t.Fatalf("seed pending_proposals row: %v", err)
	}
}

// migration000085ProposalDedupIndexUsedByQueryExplainPlan runs the isolated
// single-table form of pgDecisionsPendingOutcomeReview's dedup NOT EXISTS
// predicate (internal/scheduler/cognitive_jobs.go): same three
// equality/expression conditions, with d.id::text replaced by a literal so
// the plan can be inspected directly. Returns the joined plan text and its
// individual lines.
func migration000085ProposalDedupIndexUsedByQueryExplainPlan(t *testing.T, ctx context.Context, pool *pgxpool.Pool) (string, []string) {
	t.Helper()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `SET LOCAL enable_seqscan = off`); err != nil {
		t.Fatalf("SET LOCAL enable_seqscan = off: %v", err)
	}

	rows, err := tx.Query(
		ctx, `EXPLAIN SELECT 1 FROM pending_proposals p
		WHERE p.type = 'task'
		  AND p.proposed_by = 'scheduler:decision_outcome_review'
		  AND p.payload->>'source_entity_id' = $1`,
		"00000000-0000-0000-0000-000000000000",
	)
	if err != nil {
		t.Fatalf("EXPLAIN query: %v", err)
	}
	var planLines []string
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			rows.Close()
			t.Fatalf("scan EXPLAIN line: %v", err)
		}
		planLines = append(planLines, line)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate EXPLAIN rows: %v", err)
	}
	plan := strings.Join(planLines, "\n")
	t.Logf("EXPLAIN plan:\n%s", plan)
	return plan, planLines
}

// migration000085ProposalDedupIndexUsedByQueryAssertIndexCond asserts all
// three predicates landed in the Index Cond line rather than falling back
// to a Filter.
func migration000085ProposalDedupIndexUsedByQueryAssertIndexCond(t *testing.T, plan string, planLines []string) {
	t.Helper()
	var indexCondLine string
	for _, line := range planLines {
		if strings.Contains(line, "Index Cond:") {
			indexCondLine = line
			break
		}
	}
	if indexCondLine == "" {
		t.Fatalf("expected an Index Cond line in the plan, got:\n%s", plan)
	}
	for _, want := range []string{
		"type = 'task'",
		"proposed_by = 'scheduler:decision_outcome_review'",
		"payload ->> 'source_entity_id'",
	} {
		if !strings.Contains(indexCondLine, want) {
			t.Errorf("Index Cond line missing %q — predicate fell back to a Filter instead of the index seek.\nIndex Cond: %s", want, indexCondLine)
		}
	}
}

// migration000085ProposalDedupIndexUsedByQueryAssertNoResidualFilter asserts
// none of the three predicates needed a residual Filter — if any of them
// does, the index isn't covering the full predicate shape.
func migration000085ProposalDedupIndexUsedByQueryAssertNoResidualFilter(t *testing.T, planLines []string) {
	t.Helper()
	for _, line := range planLines {
		if strings.Contains(line, "Filter:") {
			t.Errorf("unexpected residual Filter line (all 3 predicates should be in Index Cond): %s", line)
		}
	}
}
