//go:build integration

package db

import (
	"context"
	"flag"
	"log"
	"os"
	"sort"
	"strings"
	"testing"

	migrationfs "github.com/Wayne997035/wayneblacktea/migrations"
	"github.com/jackc/pgx/v5/pgxpool"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

// [F1003-08][F1003-09] package db (not db_test): the EXPLAIN-based proof
// tests for migrations 000086/000087 need the literal unexported
// sqlc-generated SQL text (listActiveGoals, listPendingProposals) so they
// never hand-copy a transcription of it — see Lead's supplement 1 in the
// sprint-1003 dispatch record. That requires living in this package rather
// than internal/storage (which only has the DDL/reversibility/indexdef
// proofs, in migration_000086_index_integration_test.go /
// migration_000087_index_integration_test.go).
//
// skipMigrations mirrors internal/gtd/pg_test_main_test.go's own copy — one
// per package by this repo's established convention (not shared, since
// each is a small file-local var).
var skipMigrations = map[string]bool{
	"000011_backfill_workspace_id.up.sql": true, // psql `\set` metacommand
}

var testPgPool *pgxpool.Pool

func TestMain(m *testing.M) {
	flag.Parse()
	os.Exit(run(m))
}

func run(m *testing.M) int {
	if testing.Short() {
		return m.Run()
	}
	ctx := context.Background()
	c, err := tcpostgres.Run(
		ctx,
		"pgvector/pgvector:pg16",
		tcpostgres.WithDatabase("wbt_test"),
		tcpostgres.WithUsername("wbt"),
		tcpostgres.WithPassword("wbt"),
		tcpostgres.BasicWaitStrategies(),
	)
	if err != nil {
		log.Fatalf("start postgres container: %v", err)
	}
	defer func() { _ = c.Terminate(ctx) }()

	dsn, err := c.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		log.Printf("get connection string: %v", err)
		return 1
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		log.Printf("pgxpool.New: %v", err)
		return 1
	}
	defer pool.Close()

	applied := applyAllUpMigrationsOnce(ctx, pool)
	if !applied["000086_goals_active_due_date_idx.up.sql"] || !applied["000087_pending_proposals_status_pending_sort_key.up.sql"] {
		log.Print("migrations 000086/000087 were not both applied — test would not exercise the new indexes")
		return 1
	}
	log.Printf("applyAllUpMigrations: applied %d migrations including 000086/000087", len(applied))

	testPgPool = pool
	return m.Run()
}

// applyAllUpMigrationsOnce executes every *.up.sql file in the embedded
// migrations FS in numeric (filename-sorted) order against pool, skipping
// the known-incompatible files in skipMigrations. Returns the set of
// applied filenames so callers can assert specific migrations ran.
func applyAllUpMigrationsOnce(ctx context.Context, pool *pgxpool.Pool) map[string]bool {
	entries, err := migrationfs.FS.ReadDir(".")
	if err != nil {
		log.Fatalf("read embedded migrations dir: %v", err)
	}
	var ups []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".up.sql") {
			continue
		}
		ups = append(ups, name)
	}
	sort.Strings(ups)

	applied := make(map[string]bool, len(ups))
	for _, name := range ups {
		if skipMigrations[name] {
			log.Printf("applyAllUpMigrations: skipping %s (psql-metacommand-only file)", name)
			continue
		}
		body, err := migrationfs.FS.ReadFile(name)
		if err != nil {
			log.Fatalf("read %s: %v", name, err)
		}
		if _, err := pool.Exec(ctx, string(body)); err != nil {
			log.Fatalf("apply %s: %v", name, err)
		}
		applied[name] = true
	}
	return applied
}
