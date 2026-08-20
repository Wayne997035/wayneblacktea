//go:build integration

package workspace_test

import (
	"context"
	"flag"
	"log"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/workspace"
	migrationfs "github.com/Wayne997035/wayneblacktea/migrations"
	"github.com/google/uuid"
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
	applied := applyAllUpMigrationsOnce(ctx, pool)
	if !applied["000028_repos_composite_unique.up.sql"] {
		log.Print("migration 000028 was not applied — test would not exercise per-workspace unique constraint")
		return 1
	}
	log.Printf("applyAllUpMigrations: applied %d migrations including 000028_repos_composite_unique.up.sql", len(applied))

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
		body, readErr := migrationfs.FS.ReadFile(name)
		if readErr != nil {
			log.Fatalf("read %s: %v", name, readErr)
		}
		if _, execErr := pool.Exec(ctx, string(body)); execErr != nil {
			log.Fatalf("apply %s: %v", name, execErr)
		}
		applied[name] = true
	}
	return applied
}

// openTestPgPool returns the package-level singleton pool initialised in TestMain.
// Skip with -short flag: testcontainers requires Docker and adds ~5-10 s.
func openTestPgPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping Postgres integration test in -short mode (requires Docker)")
	}
	return testPgPool
}

// TestUpsertRepo_PerWorkspaceConflict verifies that two distinct workspaces can
// each upsert a repo with the same name without conflicting. Migration 000028
// replaced the global UNIQUE(name) constraint with UNIQUE(workspace_id, name).
// Before the fix the ON CONFLICT clause targeted the wrong constraint and
// Postgres rejected the second upsert.
func TestUpsertRepo_PerWorkspaceConflict(t *testing.T) {
	pool := openTestPgPool(t)
	ctx := context.Background()

	wsA := uuid.New()
	wsB := uuid.New()
	storeA := workspace.NewStore(pool, &wsA)
	storeB := workspace.NewStore(pool, &wsB)

	repoName := "shared-repo-name"

	repoA, err := storeA.UpsertRepo(ctx, workspace.UpsertRepoParams{
		Name:          repoName,
		Path:          strPtr("/home/a/shared-repo-name"),
		Description:   strPtr("workspace A copy"),
		CurrentBranch: strPtr("main"),
	})
	if err != nil {
		t.Fatalf("storeA.UpsertRepo: %v", err)
	}
	t.Cleanup(func() {
		if _, delErr := pool.Exec(ctx, `DELETE FROM repos WHERE id = $1`, repoA.ID); delErr != nil {
			t.Logf("cleanup repoA: %v", delErr)
		}
	})

	repoB, err := storeB.UpsertRepo(ctx, workspace.UpsertRepoParams{
		Name:          repoName,
		Path:          strPtr("/home/b/shared-repo-name"),
		Description:   strPtr("workspace B copy"),
		CurrentBranch: strPtr("develop"),
	})
	if err != nil {
		t.Fatalf("storeB.UpsertRepo: %v", err)
	}
	t.Cleanup(func() {
		if _, delErr := pool.Exec(ctx, `DELETE FROM repos WHERE id = $1`, repoB.ID); delErr != nil {
			t.Logf("cleanup repoB: %v", delErr)
		}
	})

	// Both must have returned distinct rows with their respective workspace IDs.
	if repoA.ID == repoB.ID {
		t.Errorf("expected distinct IDs for cross-workspace upsert, got same ID %s", repoA.ID)
	}

	wantA := [16]byte(wsA)
	wantB := [16]byte(wsB)
	if repoA.WorkspaceID.Bytes != wantA {
		t.Errorf("repoA.WorkspaceID = %v, want wsA=%v", repoA.WorkspaceID.Bytes, wantA)
	}
	if repoB.WorkspaceID.Bytes != wantB {
		t.Errorf("repoB.WorkspaceID = %v, want wsB=%v", repoB.WorkspaceID.Bytes, wantB)
	}

	// Assert both repos exist as distinct rows in the DB.
	var count int
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM repos WHERE name = $1`, repoName,
	).Scan(&count); err != nil {
		t.Fatalf("count repos by name: %v", err)
	}
	if count != 2 {
		t.Errorf("expected 2 distinct rows for name=%q, got %d", repoName, count)
	}
}

// TestUpsertRepo_SameWorkspaceUpdate verifies the ON CONFLICT DO UPDATE path:
// a second upsert with the same (workspace_id, name) must return the same row
// ID and reflect updated field values.
func TestUpsertRepo_SameWorkspaceUpdate(t *testing.T) {
	pool := openTestPgPool(t)
	ctx := context.Background()

	wsID := uuid.New()
	store := workspace.NewStore(pool, &wsID)

	repoName := "my-project"

	first, err := store.UpsertRepo(ctx, workspace.UpsertRepoParams{
		Name:            repoName,
		Path:            strPtr("/repos/my-project"),
		Description:     strPtr("initial description"),
		CurrentBranch:   strPtr("main"),
		NextPlannedStep: strPtr("setup CI"),
	})
	if err != nil {
		t.Fatalf("first UpsertRepo: %v", err)
	}
	t.Cleanup(func() {
		if _, delErr := pool.Exec(ctx, `DELETE FROM repos WHERE id = $1`, first.ID); delErr != nil {
			t.Logf("cleanup repo: %v", delErr)
		}
	})

	second, err := store.UpsertRepo(ctx, workspace.UpsertRepoParams{
		Name:            repoName,
		Path:            strPtr("/repos/my-project"),
		Description:     strPtr("updated description"),
		CurrentBranch:   strPtr("feature/x"),
		NextPlannedStep: strPtr("write tests"),
	})
	if err != nil {
		t.Fatalf("second UpsertRepo: %v", err)
	}

	// Same row: IDs must match.
	if first.ID != second.ID {
		t.Errorf("expected same ID on conflict-update: first=%s second=%s", first.ID, second.ID)
	}

	// Description and CurrentBranch must reflect the second call's values.
	if second.Description.String != "updated description" {
		t.Errorf("Description not updated: got %q, want %q", second.Description.String, "updated description")
	}
	if second.CurrentBranch.String != "feature/x" {
		t.Errorf("CurrentBranch not updated: got %q, want %q", second.CurrentBranch.String, "feature/x")
	}
	if second.NextPlannedStep.String != "write tests" {
		t.Errorf("NextPlannedStep not updated: got %q, want %q", second.NextPlannedStep.String, "write tests")
	}

	// Exactly one row must exist for this (workspace_id, name) pair.
	var count int
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM repos WHERE workspace_id = $1 AND name = $2`, wsID, repoName,
	).Scan(&count); err != nil {
		t.Fatalf("count repos: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 row after upsert-update, got %d", count)
	}
}
