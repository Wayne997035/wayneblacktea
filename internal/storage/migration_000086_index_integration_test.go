//go:build integration

package storage

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// [F1003-08] Postgres DDL/reversibility coverage for migration 000086 — see
// migrations/000086_goals_active_due_date_idx.up.sql for the index shape
// and rationale, and internal/storage/sqlite/migration_000086_index_test.go
// for the SQLite twin. package storage (not storage_test), same build-tag
// `integration` file group as repo_name_cleanup_migration_integration_test.go,
// so this file reuses that file's TestMain / testAdminDSN / newIsolatedTestDB /
// migrateToVersion — do NOT declare a second TestMain in this package.
// The EXPLAIN-based proof that the index is actually usable (custom vs.
// generic plan, cost-threshold judgment) lives in package db
// (internal/db/migration_000086_explain_test.go) because it needs the
// unexported sqlc-generated listActiveGoals SQL text — this file only
// proves existence, exact shape, and up/down/up reversibility.

// wantIdx000086Def is the exact expected pg_indexes.indexdef text — verified
// against a real Postgres 16 instance (pgvector/pgvector:pg16, same image
// this package's TestMain uses), not assumed. PG normalizes away the
// default ASC NULLS LAST on the due_date column, same normalization style
// documented at internal/snapshot/migration_000081_indexes_test.go.
const wantIdx000086Def = "CREATE INDEX idx_goals_active_due_date " +
	"ON public.goals USING btree (workspace_id, due_date, id) WHERE (status = 'active'::text)"

// TestMigration000086_GoalsActiveDueDateIndexExists proves migration 000086
// adds idx_goals_active_due_date to Postgres with the exact expected shape
// (table, column order, partial predicate) and that down/up is reversible.
func TestMigration000086_GoalsActiveDueDateIndexExists(t *testing.T) {
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

	var existsBefore bool
	if err := pool.QueryRow(
		ctx,
		`SELECT EXISTS (SELECT 1 FROM pg_indexes WHERE indexname = $1)`,
		"idx_goals_active_due_date",
	).Scan(&existsBefore); err != nil {
		t.Fatalf("check index before migration: %v", err)
	}
	if existsBefore {
		t.Fatal("idx_goals_active_due_date exists before migration 000086 has run")
	}

	t.Setenv("WBT_AUTO_MIGRATE", "")
	if err := RunMigrations(ctx, dsn); err != nil {
		t.Fatalf("RunMigrations to 86: %v", err)
	}

	var got string
	if err := pool.QueryRow(
		ctx,
		`SELECT indexdef FROM pg_indexes WHERE indexname = $1`,
		"idx_goals_active_due_date",
	).Scan(&got); err != nil {
		t.Fatalf("query pg_indexes: %v", err)
	}
	if got != wantIdx000086Def {
		t.Errorf("indexdef mismatch:\n got:  %s\n want: %s", got, wantIdx000086Def)
	}

	// Reversibility: down to 85 removes it, re-up to 86 recreates it
	// identically (mirrors TestMigration000085_ProposalDedupIndexExists).
	migrateToVersion(t, dsn, 85)
	var existsAfterDown bool
	if err := pool.QueryRow(
		ctx,
		`SELECT EXISTS (SELECT 1 FROM pg_indexes WHERE indexname = $1)`,
		"idx_goals_active_due_date",
	).Scan(&existsAfterDown); err != nil {
		t.Fatalf("check index after down: %v", err)
	}
	if existsAfterDown {
		t.Error("idx_goals_active_due_date still exists after the down migration")
	}

	migrateToVersion(t, dsn, 86)
	var gotAfterReup string
	if err := pool.QueryRow(
		ctx,
		`SELECT indexdef FROM pg_indexes WHERE indexname = $1`,
		"idx_goals_active_due_date",
	).Scan(&gotAfterReup); err != nil {
		t.Fatalf("query pg_indexes after re-up: %v", err)
	}
	if gotAfterReup != wantIdx000086Def {
		t.Errorf("indexdef mismatch after re-up:\n got:  %s\n want: %s", gotAfterReup, wantIdx000086Def)
	}
}
