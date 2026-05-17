package gtd_test

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
// runner. They contain psql metacommands (`\set`) that pgx (and golang-migrate
// when fed plain SQL) cannot parse. Production handles them via manual
// `psql -f` after substitution. Documented as "NOT AUTO-RUN" in the migration
// file header.
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
	// pgvector/pgvector:pg16 is the upstream image with the `vector` extension
	// pre-installed. Migration 000005_knowledge.up.sql does
	// `CREATE EXTENSION vector` so the vanilla `postgres:16-alpine` image
	// fails with `extension "vector" is not available`.
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

	// Apply migrations with a custom runner that skips the documented
	// "NOT AUTO-RUN" psql-metacommand files (see skipMigrations comment).
	// This still applies migration 000026 end-to-end — the whole point of
	// the test is to verify GTDStore.DeleteTask performs the cascade in
	// code, not by leaning on a leftover FK.
	applied := applyAllUpMigrationsOnce(ctx, pool)
	if !applied["000026_drop_fk_constraints.up.sql"] {
		log.Print("migration 000026 was not applied — test would not exercise FK-drop cascade")
		return 1
	}
	log.Printf("applyAllUpMigrations: applied %d migrations including 000026_drop_fk_constraints.up.sql", len(applied))

	testPgPool = pool
	return m.Run()
}

// applyAllUpMigrationsOnce executes every *.up.sql file in the embedded
// migrations FS in numeric (filename-sorted) order against pool, skipping the
// known-incompatible files in skipMigrations. Returns the set of applied
// filenames so callers can assert specific migrations ran.
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
