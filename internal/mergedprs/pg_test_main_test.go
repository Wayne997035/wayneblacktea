package mergedprs_test

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

// skipMigrations are .up.sql files that MUST NOT be applied by the test
// runner (they use psql metacommands pgx cannot parse). Mirrors the same
// list maintained by other package test mains.
var skipMigrations = map[string]bool{
	"000011_backfill_workspace_id.up.sql": true,
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
	c, err := tcpostgres.Run(ctx,
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

	if err := applyAllUpMigrationsOnce(ctx, pool); err != nil {
		log.Printf("apply migrations: %v", err)
		return 1
	}

	testPgPool = pool
	return m.Run()
}

// applyAllUpMigrationsOnce executes every *.up.sql in numeric order, skipping
// known-incompatible files. Returns first error or nil.
func applyAllUpMigrationsOnce(ctx context.Context, pool *pgxpool.Pool) error {
	entries, err := migrationfs.FS.ReadDir(".")
	if err != nil {
		return err
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

	for _, name := range ups {
		if skipMigrations[name] {
			continue
		}
		body, err := migrationfs.FS.ReadFile(name)
		if err != nil {
			return err
		}
		if _, err := pool.Exec(ctx, string(body)); err != nil {
			return err
		}
	}
	return nil
}
